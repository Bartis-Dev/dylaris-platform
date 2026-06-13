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

// ScheduledTasksHandler exposes per-server cron task CRUD. Permissions: only
// the owner/admin can mutate (we treat scheduled tasks as a "power"-class
// action — restart a server, broadcast as the server). Read access matches
// the same gate.

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

// validTaskTypes pins V1 to the documented two. P9 will extend with "rcon"
// (run a single rcon command on a schedule).
var validTaskTypes = map[string]bool{"restart": true, "say": true}

func (h *ScheduledTasksHandler) canAccess(r *http.Request, serverID int) (*models.Server, bool) {
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		return nil, false
	}
	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)
	if !checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "power") {
		return nil, false
	}
	return srv, true
}

func (h *ScheduledTasksHandler) List(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if _, ok := h.canAccess(r, serverID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
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
	if _, ok := h.canAccess(r, serverID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req scheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ScheduleCron = strings.TrimSpace(req.ScheduleCron)
	req.Payload = strings.TrimSpace(req.Payload)
	// Strip CR/LF so a "say" payload can't smuggle extra console commands via
	// newline injection once the Node forwards it to the server stdin, and cap
	// lengths so a task row can't carry an unbounded blob.
	req.Payload = strings.ReplaceAll(strings.ReplaceAll(req.Payload, "\r", ""), "\n", "")
	if len(req.Name) > 128 {
		sendJSONError(w, "Name too long (max 128 characters)", http.StatusBadRequest)
		return
	}
	if len(req.Payload) > 512 {
		sendJSONError(w, "Payload too long (max 512 characters)", http.StatusBadRequest)
		return
	}
	if !validTaskTypes[req.TaskType] {
		sendJSONError(w, "Unsupported task type", http.StatusBadRequest)
		return
	}
	if req.TaskType == "say" && req.Payload == "" {
		sendJSONError(w, "Payload (message) required for 'say' task", http.StatusBadRequest)
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
	if _, ok := h.canAccess(r, serverID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
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
	if req.TaskType != "" && !validTaskTypes[req.TaskType] {
		sendJSONError(w, "Unsupported task type", http.StatusBadRequest)
		return
	}
	if req.Name != "" {
		existing.Name = strings.TrimSpace(req.Name)
	}
	if req.TaskType != "" {
		existing.TaskType = req.TaskType
	}
	if req.Payload != "" {
		existing.Payload = strings.TrimSpace(req.Payload)
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
	if _, ok := h.canAccess(r, serverID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
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
