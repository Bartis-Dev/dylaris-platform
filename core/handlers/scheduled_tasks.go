package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"

	"github.com/gorilla/mux"
)

// ScheduledTasksHandler exposes per-server cron task CRUD. Access is enforced
// by the RequireCap chokepoint in routes.go (schedule.read/write/delete).

type ScheduledTasksHandler struct {
	state *AppState
}

func NewScheduledTasksHandler(state *AppState) *ScheduledTasksHandler {
	return &ScheduledTasksHandler{state: state}
}

type scheduledTaskRequest struct {
	Name         string `json:"name"`
	TaskType     string `json:"taskType"`
	ScheduleCron string `json:"scheduleCron"`
	Payload      string `json:"payload"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

// Task types pinned to restart + say. RCON-via-cron was considered but kept
// out: external API keys + the panel RCON tab already cover scheduled-RCON use
// cases without a third execution path.
var validTaskTypes = map[string]bool{"restart": true, "say": true}

const (
	scheduledTaskMaxName    = 128
	scheduledTaskMaxPayload = 512
)

// normalizeTaskName and normalizeTaskPayload are the ONE place either field is
// cleaned. They exist because Create did all of this inline and Update did none
// of it - it only TrimSpace'd the payload - so a PATCH could store what a POST
// refuses.
//
// The payload becomes "say " + payload on the server's stdin queue, so an
// embedded newline is a second console command. Today the log-shipper strips
// CR/LF again before writing to the JVM's stdin, which is what kept the PATCH
// gap from being a live console-command injection for anyone holding
// schedule.write but not console.send. That is one line in a different service
// standing between a stored payload and command execution; the value must not
// carry a newline in the first place.
func normalizeTaskPayload(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
	return strings.TrimSpace(s)
}

func normalizeTaskName(s string) string {
	return strings.TrimSpace(s)
}

// validateTaskFields runs the checks both Create and Update need, against the
// already-normalized values. Returns "" when the task is acceptable.
func validateTaskFields(name, taskType, payload string) string {
	if len(name) > scheduledTaskMaxName {
		return "Name too long (max 128 characters)"
	}
	if len(payload) > scheduledTaskMaxPayload {
		return "Payload too long (max 512 characters)"
	}
	if !validTaskTypes[taskType] {
		return "Unsupported task type"
	}
	// A task flipped to "say" with nothing to say errors on every tick forever.
	// Create refuses it; Update could reach it by changing only the type.
	if taskType == "say" && payload == "" {
		return "Payload (message) required for 'say' task"
	}
	return ""
}

func (h *ScheduledTasksHandler) List(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	tasks, err := h.state.Store.ListScheduledTasksByServer(serverID)
	if err != nil {
		sendJSONError(w, "Failed to load tasks", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"tasks":   tasks,
	})
}

func (h *ScheduledTasksHandler) Create(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	var req scheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = normalizeTaskName(req.Name)
	req.ScheduleCron = strings.TrimSpace(req.ScheduleCron)
	req.Payload = normalizeTaskPayload(req.Payload)
	if msg := validateTaskFields(req.Name, req.TaskType, req.Payload); msg != "" {
		sendJSONError(w, msg, http.StatusBadRequest)
		return
	}
	next, err := services.ComputeNextRun(req.ScheduleCron, time.Now().UTC())
	if err != nil {
		sendJSONError(w, "Invalid cron expression", http.StatusBadRequest)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	userID, _ := r.Context().Value("userID").(string)
	var createdBy *string
	if userID != "" {
		v := userID
		createdBy = &v
	}

	t := &models.ScheduledTask{
		ServerID:     serverID,
		Name:         req.Name,
		TaskType:     req.TaskType,
		ScheduleCron: req.ScheduleCron,
		Payload:      req.Payload,
		Enabled:      enabled,
		NextRun:      &next,
		CreatedBy:    createdBy,
	}
	id, err := h.state.Store.CreateScheduledTask(t)
	if err != nil {
		sendJSONError(w, "Failed to create task", http.StatusInternalServerError)
		return
	}
	created, _ := h.state.Store.GetScheduledTask(id)
	if created == nil {
		t.ID = id
		created = t
	}

	h.state.Events.Publish(r.Context(), "scheduled_tasks.changed", nil)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task":    created,
	})
}

func (h *ScheduledTasksHandler) Update(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	taskID, _ := strconv.Atoi(mux.Vars(r)["taskId"])
	existing, err := h.state.Store.GetScheduledTask(taskID)
	if err != nil || existing == nil || existing.ServerID != serverID {
		sendJSONError(w, "Task not found", http.StatusNotFound)
		return
	}
	var req scheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	// Apply the patch onto a copy first, then validate the RESULT with the same
	// rules Create uses. Validating the request alone would miss the combination
	// that only a patch can reach - changing taskType to "say" while leaving the
	// existing empty payload in place.
	if req.Name != "" {
		existing.Name = normalizeTaskName(req.Name)
	}
	if req.TaskType != "" {
		existing.TaskType = req.TaskType
	}
	if req.Payload != "" {
		existing.Payload = normalizeTaskPayload(req.Payload)
	}
	if msg := validateTaskFields(existing.Name, existing.TaskType, existing.Payload); msg != "" {
		sendJSONError(w, msg, http.StatusBadRequest)
		return
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.ScheduleCron != "" && req.ScheduleCron != existing.ScheduleCron {
		next, err := services.ComputeNextRun(req.ScheduleCron, time.Now().UTC())
		if err != nil {
			sendJSONError(w, "Invalid cron expression", http.StatusBadRequest)
			return
		}
		existing.ScheduleCron = req.ScheduleCron
		existing.NextRun = &next
	}
	// If we're (re-)enabling a task that lost its next_run, compute one.
	if existing.Enabled && existing.NextRun == nil {
		next, err := services.ComputeNextRun(existing.ScheduleCron, time.Now().UTC())
		if err == nil {
			existing.NextRun = &next
		}
	}
	if !existing.Enabled {
		existing.NextRun = nil
	}
	if err := h.state.Store.UpdateScheduledTask(existing); err != nil {
		sendJSONError(w, "Failed to save task", http.StatusInternalServerError)
		return
	}

	h.state.Events.Publish(r.Context(), "scheduled_tasks.changed", nil)

	updated, _ := h.state.Store.GetScheduledTask(taskID)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task":    updated,
	})
}

func (h *ScheduledTasksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	taskID, _ := strconv.Atoi(mux.Vars(r)["taskId"])
	existing, err := h.state.Store.GetScheduledTask(taskID)
	if err != nil || existing == nil || existing.ServerID != serverID {
		sendJSONError(w, "Task not found", http.StatusNotFound)
		return
	}
	if err := h.state.Store.DeleteScheduledTask(taskID); err != nil {
		sendJSONError(w, "Failed to delete", http.StatusInternalServerError)
		return
	}

	h.state.Events.Publish(r.Context(), "scheduled_tasks.changed", nil)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ValidateCron POST /api/scheduled-tasks/validate — used by the panel to
// preview the next-run timestamp for a cron string before the user saves.
// No server scope here — the cron preview is a pure transform with no
// side effects. Body: {scheduleCron}. Response: {success, valid, nextRun}.
func (h *ScheduledTasksHandler) ValidateCron(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ScheduleCron string `json:"scheduleCron"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	next, err := services.ComputeNextRun(strings.TrimSpace(req.ScheduleCron), time.Now().UTC())
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"valid":   false,
			"error":   err.Error(),
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"valid":   true,
		"nextRun": next,
	})
}
