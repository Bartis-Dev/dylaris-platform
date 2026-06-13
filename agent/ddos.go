package agent

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

type AlertLevel string

const (
	AlertNormal   AlertLevel = "normal"
	AlertWarning  AlertLevel = "warning"
	AlertCritical AlertLevel = "critical"
)

type DDoSThresholds struct {
	PpsWarning  uint64 `json:"pps_warning"`
	PpsCritical uint64 `json:"pps_critical"`
	CpsWarning  uint64 `json:"cps_warning"`
	CpsCritical uint64 `json:"cps_critical"`
}

type AttackEvent struct {
	StartedAt   int64  `json:"started_at"`
	EndedAt     int64  `json:"ended_at"`
	PeakPps     uint64 `json:"peak_pps"`
	DurationSec int64  `json:"duration_sec"`
}

type DDoSStatus struct {
	Level         AlertLevel     `json:"level"`
	StartedAt     *int64         `json:"started_at,omitempty"`
	DurationSec   int64          `json:"duration_sec"`
	PeakPps       uint64         `json:"peak_pps"`
	CurrentPps    uint64         `json:"current_pps"`
	Thresholds    DDoSThresholds `json:"thresholds"`
	RecentAttacks []AttackEvent  `json:"recent_attacks"`
}

type ddosPersistentData struct {
	Thresholds    DDoSThresholds `json:"thresholds"`
	RecentAttacks []AttackEvent  `json:"recent_attacks"`
}

type DDoSDetector struct {
	mu          sync.Mutex
	thresholds  DDoSThresholds
	level       AlertLevel
	attackStart *time.Time
	peakPps     uint64
	currentPps  uint64

	recentAttacks []AttackEvent
	dataFile      string
}

func DefaultThresholds() DDoSThresholds {
	return DDoSThresholds{
		PpsWarning:  50000,
		PpsCritical: 200000,
		CpsWarning:  1000,
		CpsCritical: 5000,
	}
}

func NewDDoSDetector(dataFile string) *DDoSDetector {
	d := &DDoSDetector{
		thresholds:    DefaultThresholds(),
		level:         AlertNormal,
		recentAttacks: make([]AttackEvent, 0),
		dataFile:      dataFile,
	}
	d.loadData()
	return d
}

// Evaluate is called every second with current packet stats.
func (d *DDoSDetector) Evaluate(stats PacketStats) {
	d.mu.Lock()
	defer d.mu.Unlock()

	totalPps := stats.PpsIn + stats.PpsOut
	d.currentPps = totalPps

	// Determine alert level
	var newLevel AlertLevel
	switch {
	case totalPps >= d.thresholds.PpsCritical || stats.CpsIn >= d.thresholds.CpsCritical:
		newLevel = AlertCritical
	case totalPps >= d.thresholds.PpsWarning || stats.CpsIn >= d.thresholds.CpsWarning:
		newLevel = AlertWarning
	default:
		newLevel = AlertNormal
	}

	now := time.Now()

	// State transitions
	if newLevel != AlertNormal && d.level == AlertNormal {
		// Attack started
		d.attackStart = &now
		d.peakPps = totalPps
	} else if newLevel == AlertNormal && d.level != AlertNormal {
		// Attack ended
		if d.attackStart != nil {
			event := AttackEvent{
				StartedAt:   d.attackStart.Unix(),
				EndedAt:     now.Unix(),
				PeakPps:     d.peakPps,
				DurationSec: int64(now.Sub(*d.attackStart).Seconds()),
			}
			d.recentAttacks = append(d.recentAttacks, event)
			// Keep last 100 attacks
			if len(d.recentAttacks) > 100 {
				d.recentAttacks = d.recentAttacks[len(d.recentAttacks)-100:]
			}
			d.saveDataLocked()
		}
		d.attackStart = nil
		d.peakPps = 0
	}

	// Update peak during attack
	if d.attackStart != nil && totalPps > d.peakPps {
		d.peakPps = totalPps
	}

	d.level = newLevel
}

func (d *DDoSDetector) GetStatus() DDoSStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	status := DDoSStatus{
		Level:         d.level,
		PeakPps:       d.peakPps,
		CurrentPps:    d.currentPps,
		Thresholds:    d.thresholds,
		RecentAttacks: d.recentAttacks,
	}

	if d.attackStart != nil {
		ts := d.attackStart.Unix()
		status.StartedAt = &ts
		status.DurationSec = int64(time.Since(*d.attackStart).Seconds())
	}

	return status
}

func (d *DDoSDetector) SetThresholds(t DDoSThresholds) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.thresholds = t
	d.saveDataLocked()
}

func (d *DDoSDetector) GetThresholds() DDoSThresholds {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.thresholds
}

func (d *DDoSDetector) loadData() {
	file, err := os.ReadFile(d.dataFile)
	if err != nil {
		return
	}
	var pd ddosPersistentData
	if err := json.Unmarshal(file, &pd); err != nil {
		return
	}
	// Only load thresholds if they were set (non-zero)
	if pd.Thresholds.PpsWarning > 0 {
		d.thresholds = pd.Thresholds
	}
	if pd.RecentAttacks != nil {
		d.recentAttacks = pd.RecentAttacks
	}
}

func (d *DDoSDetector) saveDataLocked() {
	pd := ddosPersistentData{
		Thresholds:    d.thresholds,
		RecentAttacks: d.recentAttacks,
	}
	data, err := json.Marshal(pd)
	if err != nil {
		log.Printf("agent: marshal ddos data: %v", err)
		return
	}
	// Atomic write: stage to a temp file then rename over the target so a crash
	// mid-write can't leave a truncated/corrupt data file.
	tmp := d.dataFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Printf("agent: write ddos data: %v", err)
		return
	}
	if err := os.Rename(tmp, d.dataFile); err != nil {
		log.Printf("agent: rename ddos data: %v", err)
		os.Remove(tmp)
	}
}
