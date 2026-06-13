package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	batchSize     = 50
	batchInterval = 200 * time.Millisecond
	maxStreamLen  = 1000
	lineChanSize  = 512
	// Key TTL: long enough that a low-GC-frequency server (large heap, idle
	// MC) still has a fresh-ish value when the node samples at 2s. If GC
	// hasn't fired in 5min the value is effectively stale anyway, and the
	// panel falls back to the container metric.
	heapKeyTTL = 5 * time.Minute
)

// gcLineRegex matches the GC summary lines emitted by both Java 9+
// unified logging (`-Xlog:gc`) and the legacy Java-8 `-XX:+PrintGCDetails`
// format. Captures: before-GC, after-GC, total -- each with a K or M
// unit suffix.
//
//	Java 17 G1: "[...][info][gc] GC(0) Pause Young (Normal) ... 256M->50M(2048M) 12.345ms"
//	Java 8 PG:  "[GC (Allocation Failure)  256000K->50000K(2048000K), 0.012345 secs]"
var gcLineRegex = regexp.MustCompile(`(\d+)([KM])->(\d+)([KM])\((\d+)([KM])\)`)

// parseHeapAfterGC extracts the post-GC live-heap size (in MB) from a
// JVM GC log line. Returns 0 + false if no GC summary is on the line.
func parseHeapAfterGC(line string) (int64, bool) {
	m := gcLineRegex.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	val, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil {
		return 0, false
	}
	if m[4] == "K" {
		val /= 1024
	}
	// Reject zero/negative: a 0 MB heap "value" right after a Full GC
	// would publish a misleading dip to zero in the live chart. Skipping
	// it lets the previous value stand until a meaningful GC happens.
	if val <= 0 {
		return 0, false
	}
	return val, true
}

// isUnifiedGCLine reports whether a stdout line is a JVM unified-logging
// GC summary that we should hide from the user-facing console. The lines
// only exist because the platform injects `-Xlog:gc::utctime,level,tags`
// to feed the live heap metric -- the operator never asked for them and
// they drown out the actual MC server output. We still parse the heap
// number out before filtering, so the metric stays accurate.
//
// The unified-logging format is `[<timestamp>][<level>][gc] ...` (and
// `][gc,heap]`, `][gc,start]` etc. variants). Substring `][gc` is unique
// enough; MC's own log4j format uses `[Server thread/INFO]:` so there's
// no overlap. Java 8's legacy `[GC (...)]` format is not filtered: we
// don't inject -Xlog on Java 8 anyway, so anything in that shape is
// user-requested and should pass through.
func isUnifiedGCLine(line string) bool {
	return strings.Contains(line, "][gc")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// uuidRegex / subServerRegex validate the env vars that get interpolated into
// Redis keys. Only Node sets these today, but unvalidated input could collide
// key namespaces (e.g. SUB_SERVER=foo:logs) or inject separators.
var (
	uuidRegex      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	subServerRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

func connectRedis() *redis.Client {
	addr := getEnv("REDIS_ADDR", "localhost:6379")
	user := getEnv("REDIS_USER", "")
	pass := getEnv("REDIS_PASS", "")
	dbStr := getEnv("REDIS_DB", "0")
	db, _ := strconv.Atoi(dbStr)

	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Username: user,
		Password: pass,
		DB:       db,
	})

	// Retry-Loop mit Exponential Backoff bis max 30s
	backoff := 1 * time.Second
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			log.Printf("log-shipper: connected to Redis at %s", addr)
			return rdb
		}
		log.Printf("log-shipper: Redis not reachable (%v), retrying in %s...", err, backoff)
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// applyCarriageReturns simulates terminal CR behavior.
// \r resets the cursor to column 0; the following text overwrites from the start.
func applyCarriageReturns(line string) string {
	if !strings.ContainsRune(line, '\r') {
		return line
	}
	parts := strings.Split(line, "\r")
	result := ""
	for _, part := range parts {
		if len(part) >= len(result) {
			result = part
		} else {
			result = part + result[len(part):]
		}
	}
	return result
}

// scanLines reads from r line by line and sends each line to ch.
func scanLines(r io.Reader, ch chan<- string) {
	scanner := bufio.NewScanner(r)
	// Default scanner buffer is 64KB; a single oversized log line trips
	// bufio.ErrTooLong, the goroutine returns, and the console freezes with
	// no diagnostic. Raise the cap to 1MB and surface the error.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		ch <- applyCarriageReturns(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Printf("log-shipper: scanner error (line >1MB or read failure): %v", err)
		ch <- "[log-shipper: a log line exceeded 1MB and was dropped]"
	}
}

// shipLogs batches log lines and writes them to the Redis Stream.
// Flushes every 200ms or when 50 lines accumulate, whichever comes first.
// Side effect: also scans each line for JVM GC summaries and updates a
// per-server Redis key with the latest post-GC heap size so the node
// stats collector can surface live heap usage to the panel.
func shipLogs(ctx context.Context, rdb *redis.Client, streamKey, heapKey string, lineCh <-chan string) {
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()

	var buf []string

	flush := func() {
		if len(buf) == 0 {
			return
		}
		buildPipe := func() redis.Pipeliner {
			pipe := rdb.Pipeline()
			for _, line := range buf {
				pipe.XAdd(ctx, &redis.XAddArgs{
					Stream: streamKey,
					MaxLen: maxStreamLen,
					Approx: true,
					Values: map[string]interface{}{"line": line},
				})
			}
			return pipe
		}
		_, err := buildPipe().Exec(ctx)
		if err != nil && ctx.Err() == nil {
			// 1x Retry on a fresh pipeline: go-redis resets a pipeline after
			// Exec, so re-running the same object would send nothing and
			// silently drop the batch.
			if _, retryErr := buildPipe().Exec(ctx); retryErr != nil {
				log.Printf("log-shipper: Redis write failed (dropping %d lines): %v", len(buf), retryErr)
			}
		}
		buf = buf[:0]
	}

	for {
		select {
		case line, ok := <-lineCh:
			if !ok {
				flush()
				return
			}
			// JVM heap accounting: pulls "100M->50M(2048M)" out of GC
			// summary lines and stores the post-GC value. Container-level
			// stats from Docker are anon-RSS = Xms (since we force
			// Xms=Xmx) and so look pinned at the limit forever; this is
			// the actual live heap.
			if heapKey != "" {
				if mb, ok := parseHeapAfterGC(line); ok {
					// Non-blocking: a slow Redis must not stall the log
					// shipper, so we fire-and-forget. Stale values fall
					// out via TTL.
					rdb.Set(ctx, heapKey, mb, heapKeyTTL)
				}
			}
			// Hide JVM GC log noise from the user console. Parsing above
			// still runs so the heap chart keeps updating; only the
			// stream-shipping is skipped. Without -Xlog the JVM is silent
			// here anyway, so this is purely about not exposing platform-
			// internal logs to the operator.
			if isUnifiedGCLine(line) {
				continue
			}
			buf = append(buf, line)
			if len(buf) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

// forwardInput reads commands from the Redis input queue and writes them to Java stdin.
func forwardInput(ctx context.Context, rdb *redis.Client, inputKey string, stdin io.WriteCloser) {
	for {
		result, err := rdb.BLPop(ctx, 2*time.Second, inputKey).Result()
		if err == redis.Nil {
			// Timeout — try again
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("log-shipper: BLPop error: %v, retrying in 1s...", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if len(result) >= 2 {
			// Strip embedded CR/LF so one queue entry can't smuggle extra
			// console commands into the server stdin (newline injection).
			line := strings.ReplaceAll(strings.ReplaceAll(result[1], "\r", ""), "\n", "")
			// Cap at 1KB: a single console command is never legitimately larger.
			if len(line) > 1024 {
				line = line[:1024]
			}
			if _, err := fmt.Fprint(stdin, line+"\n"); err != nil {
				log.Printf("log-shipper: stdin write error: %v", err)
				return
			}
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: log-shipper <command> [args...]\nExample: log-shipper java -Xmx2048M -jar /data/server/server.jar nogui")
	}

	serverUUID := getEnv("SERVER_UUID", "")
	if serverUUID == "" {
		log.Fatal("log-shipper: SERVER_UUID environment variable is required")
	}
	if !uuidRegex.MatchString(serverUUID) {
		log.Fatalf("log-shipper: SERVER_UUID %q is not a valid UUID", serverUUID)
	}

	subServer := getEnv("SUB_SERVER", "")
	if subServer != "" && !subServerRegex.MatchString(subServer) {
		log.Fatalf("log-shipper: SUB_SERVER %q must match [A-Za-z0-9_-]{1,64}", subServer)
	}
	streamKey := fmt.Sprintf("dylaris:server:%s:logs", serverUUID)
	if subServer != "" {
		streamKey = fmt.Sprintf("dylaris:server:%s:logs:%s", serverUUID, subServer)
	}
	// Heap key holds the latest post-GC live-heap size (in MB) parsed
	// off the JVM's stdout. Only one sub-server is active at a time and
	// the container restarts on switch, so a single unified key is
	// enough -- a fresh log-shipper instance overwrites stale values.
	heapKey := fmt.Sprintf("dylaris:server:%s:java-heap", serverUUID)
	inputKey := fmt.Sprintf("dylaris:server:%s:input", serverUUID)

	rdb := connectRedis()
	defer rdb.Close()

	// Check if working directory exists (may have been renamed by user)
	cwd, _ := os.Getwd()
	if _, err := os.Stat(cwd); err != nil && os.IsNotExist(err) {
		msg := fmt.Sprintf("[log-shipper] Working directory %s does not exist — server folder may have been renamed. Container will stop.", cwd)
		log.Print(msg)
		rdb.XAdd(context.Background(), &redis.XAddArgs{
			Stream: streamKey,
			MaxLen: maxStreamLen,
			Approx: true,
			Values: map[string]interface{}{"line": msg},
		})
		os.Exit(0)
	}

	stopKey := fmt.Sprintf("dylaris:server:%s:stop-requested", serverUUID)

	// Restart loop: re-launches Java on crash, exits on manual stop.
	for {
		javaCmd := exec.Command(os.Args[1], os.Args[2:]...)
		javaCmd.Env = append(os.Environ(), "TERM=xterm-256color")
		javaCmd.Stdin = nil // stdin managed via pipe below

		stdoutPipe, err := javaCmd.StdoutPipe()
		if err != nil {
			log.Fatalf("log-shipper: StdoutPipe error: %v", err)
		}
		stderrPipe, err := javaCmd.StderrPipe()
		if err != nil {
			log.Fatalf("log-shipper: StderrPipe error: %v", err)
		}
		stdinPipe, err := javaCmd.StdinPipe()
		if err != nil {
			log.Fatalf("log-shipper: StdinPipe error: %v", err)
		}

		if err := javaCmd.Start(); err != nil {
			log.Fatalf("log-shipper: failed to start process %q: %v", os.Args[1], err)
		}
		log.Printf("log-shipper: started PID %d: %s", javaCmd.Process.Pid, strings.Join(os.Args[1:], " "))

		ctx, cancel := context.WithCancel(context.Background())

		lineCh := make(chan string, lineChanSize)

		var scanWG sync.WaitGroup
		scanWG.Add(2)

		go func() { defer scanWG.Done(); scanLines(stdoutPipe, lineCh) }()
		go func() { defer scanWG.Done(); scanLines(stderrPipe, lineCh) }()

		go forwardInput(ctx, rdb, inputKey, stdinPipe)
		go shipLogs(ctx, rdb, streamKey, heapKey, lineCh)

		// Wait for Java process to exit
		if err := javaCmd.Wait(); err != nil {
			log.Printf("log-shipper: process exited with error: %v", err)
		}

		// Wait for scanners to drain, then close lineCh so shipLogs flushes.
		scanWG.Wait()
		close(lineCh)
		time.Sleep(300 * time.Millisecond)
		cancel()

		exitCode := 0
		if javaCmd.ProcessState != nil {
			exitCode = javaCmd.ProcessState.ExitCode()
		}

		// Check if stop was requested via WebUI
		_, stopErr := rdb.Get(context.Background(), stopKey).Result()
		if stopErr == nil {
			// Stop flag exists -> clean exit (manual stop)
			rdb.Del(context.Background(), stopKey)
			log.Printf("log-shipper: stop requested, exiting cleanly")
			os.Exit(exitCode)
		}

		// Exit code 0 without stop flag = normal shutdown (e.g. /stop in-game)
		if exitCode == 0 {
			log.Printf("log-shipper: process exited normally (code 0), exiting")
			os.Exit(0)
		}

		// Crash recovery: restart after delay
		log.Printf("log-shipper: process crashed (exit %d), restarting in 5s...", exitCode)
		rdb.XAdd(context.Background(), &redis.XAddArgs{
			Stream: streamKey,
			MaxLen: maxStreamLen,
			Approx: true,
			Values: map[string]interface{}{"line": fmt.Sprintf("[Server crashed (exit code %d) - restarting automatically...]", exitCode)},
		})
		time.Sleep(5 * time.Second)
	}
}
