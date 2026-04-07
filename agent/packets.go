package agent

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/net"
)

type PacketStats struct {
	PpsIn        uint64            `json:"pps_in"`
	PpsOut       uint64            `json:"pps_out"`
	CpsIn        uint64            `json:"cps_in"`
	ActiveConns  int               `json:"active_conns"`
	ProtocolDist map[string]uint64 `json:"protocol_dist"`
}

type PacketHistoryPoint struct {
	Timestamp   int64             `json:"ts"`
	PpsIn       uint64            `json:"pps_in"`
	PpsOut      uint64            `json:"pps_out"`
	Cps         uint64            `json:"cps"`
	ActiveConns int               `json:"active_conns"`
	ProtocolDist map[string]uint64 `json:"proto,omitempty"`
}

type packetPersistentData struct {
	History []PacketHistoryPoint `json:"history"`
}

type PacketCollector struct {
	mu sync.Mutex

	// Previous counters for delta calculation
	lastPacketsRecv uint64
	lastPacketsSent uint64
	lastConnCount   int
	lastTime        time.Time

	// Current live values (updated every Collect())
	currentPpsIn   uint64
	currentPpsOut  uint64
	currentCps     uint64
	currentActive  int
	currentProto   map[string]uint64

	// Connection cache (net.Connections is expensive)
	connCacheTime  time.Time
	connCacheConns []net.ConnectionStat

	// Accumulator for history averaging
	accumCount   int
	accumPpsIn   uint64
	accumPpsOut  uint64
	accumCps     uint64
	accumActive  int

	// Persistence
	dataFile string
	data     packetPersistentData
}

func NewPacketCollector(dataFile string) (*PacketCollector, error) {
	netStats, err := net.IOCounters(false)
	if err != nil {
		return nil, err
	}

	pc := &PacketCollector{
		lastTime:     time.Now(),
		currentProto: make(map[string]uint64),
		dataFile:     dataFile,
	}

	if len(netStats) > 0 {
		pc.lastPacketsRecv = netStats[0].PacketsRecv
		pc.lastPacketsSent = netStats[0].PacketsSent
	}

	pc.loadData()
	if pc.data.History == nil {
		pc.data.History = make([]PacketHistoryPoint, 0)
	}

	return pc, nil
}

// Collect is called every 1 second to update live packet stats.
func (pc *PacketCollector) Collect() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	now := time.Now()
	duration := now.Sub(pc.lastTime).Seconds()
	if duration <= 0 {
		return
	}

	// PPS from IOCounters delta
	netStats, err := net.IOCounters(false)
	if err == nil && len(netStats) > 0 {
		current := netStats[0]
		recvDiff := current.PacketsRecv - pc.lastPacketsRecv
		sentDiff := current.PacketsSent - pc.lastPacketsSent

		pc.currentPpsIn = uint64(float64(recvDiff) / duration)
		pc.currentPpsOut = uint64(float64(sentDiff) / duration)

		pc.lastPacketsRecv = current.PacketsRecv
		pc.lastPacketsSent = current.PacketsSent
	}

	// Connections - cache for 2 seconds to reduce overhead
	if now.Sub(pc.connCacheTime) >= 2*time.Second {
		conns, err := net.Connections("all")
		if err == nil {
			pc.connCacheConns = conns
			pc.connCacheTime = now

			newCount := len(conns)
			if newCount > pc.lastConnCount {
				pc.currentCps = uint64(float64(newCount-pc.lastConnCount) / duration)
			} else {
				pc.currentCps = 0
			}
			pc.lastConnCount = newCount
			pc.currentActive = newCount

			// Protocol distribution from connection types
			proto := map[string]uint64{"tcp": 0, "udp": 0, "other": 0}
			for _, c := range conns {
				switch {
				case c.Type == 1: // SOCK_STREAM = TCP
					proto["tcp"]++
				case c.Type == 2: // SOCK_DGRAM = UDP
					proto["udp"]++
				default:
					proto["other"]++
				}
			}
			pc.currentProto = proto
		}
	}

	pc.lastTime = now

	// Accumulate for history averaging
	pc.accumCount++
	pc.accumPpsIn += pc.currentPpsIn
	pc.accumPpsOut += pc.currentPpsOut
	pc.accumCps += pc.currentCps
	pc.accumActive += pc.currentActive
}

func (pc *PacketCollector) GetLiveStats() PacketStats {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	proto := make(map[string]uint64)
	for k, v := range pc.currentProto {
		proto[k] = v
	}

	return PacketStats{
		PpsIn:        pc.currentPpsIn,
		PpsOut:       pc.currentPpsOut,
		CpsIn:        pc.currentCps,
		ActiveConns:  pc.currentActive,
		ProtocolDist: proto,
	}
}

// RecordHistoryPoint stores a 1-minute average snapshot.
func (pc *PacketCollector) RecordHistoryPoint() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.accumCount == 0 {
		return
	}

	point := PacketHistoryPoint{
		Timestamp:   time.Now().Unix(),
		PpsIn:       pc.accumPpsIn / uint64(pc.accumCount),
		PpsOut:      pc.accumPpsOut / uint64(pc.accumCount),
		Cps:         pc.accumCps / uint64(pc.accumCount),
		ActiveConns: pc.accumActive / pc.accumCount,
	}

	pc.data.History = append(pc.data.History, point)

	// Keep max 45000 points (~30 days)
	maxPoints := 45000
	if len(pc.data.History) > maxPoints {
		pc.data.History = pc.data.History[len(pc.data.History)-maxPoints:]
	}

	// Reset accumulators
	pc.accumCount = 0
	pc.accumPpsIn = 0
	pc.accumPpsOut = 0
	pc.accumCps = 0
	pc.accumActive = 0

	pc.saveDataLocked()
}

// GetSmartHistory returns downsampled history, same pattern as Monitor.GetSmartHistory.
func (pc *PacketCollector) GetSmartHistory(rangeSeconds int64) []PacketHistoryPoint {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if len(pc.data.History) == 0 {
		return []PacketHistoryPoint{}
	}

	cutoff := time.Now().Unix() - rangeSeconds

	var filtered []PacketHistoryPoint
	for _, p := range pc.data.History {
		if p.Timestamp >= cutoff {
			filtered = append(filtered, p)
		}
	}

	count := len(filtered)
	if count == 0 {
		return []PacketHistoryPoint{}
	}

	maxPoints := 300
	if count <= maxPoints {
		return filtered
	}

	step := count / maxPoints
	var result []PacketHistoryPoint

	for i := 0; i < count; i += step {
		end := i + step
		if end > count {
			end = count
		}

		var sumPpsIn, sumPpsOut, sumCps uint64
		var sumActive int
		chunkSize := end - i

		for j := i; j < end; j++ {
			sumPpsIn += filtered[j].PpsIn
			sumPpsOut += filtered[j].PpsOut
			sumCps += filtered[j].Cps
			sumActive += filtered[j].ActiveConns
		}

		result = append(result, PacketHistoryPoint{
			Timestamp:   filtered[i].Timestamp,
			PpsIn:       sumPpsIn / uint64(chunkSize),
			PpsOut:      sumPpsOut / uint64(chunkSize),
			Cps:         sumCps / uint64(chunkSize),
			ActiveConns: sumActive / chunkSize,
		})
	}

	return result
}

func (pc *PacketCollector) ResetHistory() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.data.History = []PacketHistoryPoint{}
	pc.saveDataLocked()
}

func (pc *PacketCollector) loadData() {
	file, err := os.ReadFile(pc.dataFile)
	if err == nil {
		json.Unmarshal(file, &pc.data)
	}
}

func (pc *PacketCollector) saveDataLocked() {
	data, _ := json.Marshal(pc.data)
	os.WriteFile(pc.dataFile, data, 0644)
}
