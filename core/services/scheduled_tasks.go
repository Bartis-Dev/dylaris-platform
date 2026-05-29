// Package-level service for Phase 8 Scheduled Tasks. Leader-gated tick reads
// due rows, dispatches the underlying action (Queue SendCommand for restart,
// console-stdin RPush for say) and stamps next_run forward via robfig/cron's
// standard 5-field parser.
package services

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

const (
	// scheduledTaskTick is how often the executor scans for due tasks. Short
	// enough that "every minute" cron tasks fire within ~tick of their due
	// time; long enough that an idle platform doesn't spin DB queries.
	scheduledTaskTick = 30 * time.Second

	// scheduledTaskBatchLimit caps tasks dispatched per tick so a stale
	// backlog (e.g. Core was down for a day with hourly tasks queued) can't
	// flood the queue in a single sweep.
	scheduledTaskBatchLimit = 100
)

// ScheduledTaskCronParser is the cron flavour we accept from users. Standard
// 5-field UNIX cron in UTC — no seconds, no exotic descriptors. The robfig
// parser also accepts "@daily", "@hourly" etc. as a convenience.
var ScheduledTaskCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ScheduledTaskService is the leader-gated executor. Exposed at the same
// level as BackupScheduler / TicketAutoCloseService so main.go can wire it
// alongside other singletons.
type ScheduledTaskService struct {
	store  store.Store
	redis  *redis.Client
	queue  *QueueService
	events *SystemEventsPublisher
	leader leader.Election
}

func NewScheduledTaskService(s store.Store, r *redis.Client, q *QueueService, ev *SystemEventsPublisher) *ScheduledTaskService {
	return &ScheduledTaskService{store: s, redis: r, queue: q, events: ev}
}

func (s *ScheduledTaskService) SetLeader(l leader.Election) { s.leader = l }

func (s *ScheduledTaskService) Start(ctx context.Context) {
	log.Println("Scheduled Task Service started")
	go func() {
		ticker := time.NewTicker(scheduledTaskTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.leader != nil && !s.leader.IsLeader() {
					continue
				}
				s.runDue(ctx)
			}
		}
	}()
}

// ComputeNextRun parses a cron string and returns the next firing time from
// `from`. Exported so handlers can validate + preview cron strings before
// saving — they don't need to re-import the cron lib.
func ComputeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := ScheduledTaskCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron: %w", err)
	}
	return sched.Next(from), nil
}

func (s *ScheduledTaskService) runDue(ctx context.Context) {
	now := time.Now().UTC()
	due, err := s.store.ListDueScheduledTasks(now, scheduledTaskBatchLimit)
	if err != nil {
		log.Printf("scheduled-tasks: list due failed: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}

	publishServers := false
	for _, t := range due {
		err := s.execute(ctx, &t)
		next, parseErr := ComputeNextRun(t.ScheduleCron, now)
		// If the cron string somehow became invalid (e.g. someone edited
		// the DB by hand), disable the row instead of looping forever.
		var nextPtr *time.Time
		status := "ok"
		errMsg := ""
		if err != nil {
			status = "error"
			errMsg = err.Error()
		}
		if parseErr != nil {
			status = "error"
			errMsg = parseErr.Error()
			_ = s.store.SetScheduledTaskEnabled(t.ID, false, nil)
		} else {
			nextPtr = &next
		}
		if recErr := s.store.RecordScheduledTaskRun(t.ID, now, status, errMsg, nextPtr); recErr != nil {
			log.Printf("scheduled-tasks: record run for #%d failed: %v", t.ID, recErr)
		}
		if t.TaskType == "restart" {
			publishServers = true
		}
	}

	if publishServers {
		s.events.Publish(ctx, "servers.changed", nil)
	}
	// Always publish a tasks-changed event so panels watching a Scheduled
	// sub-tab refresh status/last-run/next-run without polling.
	s.events.Publish(ctx, "scheduled_tasks.changed", nil)
}

func (s *ScheduledTaskService) execute(ctx context.Context, t *models.ScheduledTask) error {
	srv, err := s.store.GetServerByID(t.ServerID)
	if err != nil {
		return fmt.Errorf("server #%d not found: %w", t.ServerID, err)
	}

	switch t.TaskType {
	case "restart":
		node, err := s.store.GetNodeByID(srv.NodeID)
		if err != nil {
			return fmt.Errorf("node for server %d not found: %w", t.ServerID, err)
		}
		if s.queue == nil {
			return fmt.Errorf("queue not available")
		}
		_ = s.store.UpdateServerDesiredState(srv.ID, "online")
		_ = s.store.UpdateServerStatus(srv.ID, "starting")
		configPayload := map[string]interface{}{"uuid": srv.UUID}
		if err := s.queue.SendCommand(ctx, node.Token, "restart", configPayload, nil); err != nil {
			return fmt.Errorf("queue restart: %w", err)
		}
		return nil

	case "say":
		if t.Payload == "" {
			return fmt.Errorf("say task has empty payload")
		}
		// Same path as the live Console "send command" handler — push into
		// the per-server stdin queue. Node forwards to the container.
		queueKey := fmt.Sprintf("dylaris:server:%s:input", srv.UUID)
		cmd := "say " + t.Payload
		if err := s.redis.RPush(ctx, queueKey, cmd).Err(); err != nil {
			return fmt.Errorf("rpush stdin: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unknown task type %q", t.TaskType)
	}
}
