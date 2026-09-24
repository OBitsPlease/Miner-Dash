package server

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

func (s *Store) ListSchedules() []protocol.Schedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.state.Schedules, func(value protocol.Schedule) string { return value.Name })
}

func (s *Store) SaveSchedule(value protocol.Schedule) (protocol.Schedule, error) {
	if strings.TrimSpace(value.Name) == "" {
		return value, errors.New("schedule name is required")
	}
	if _, err := time.Parse("15:04", value.Time); err != nil {
		return value, errors.New("schedule time must use HH:MM")
	}
	switch value.Action {
	case "start", "stop", "restart", "reboot", "shutdown":
	case "apply-flight-sheet":
		if value.FlightSheetID == "" {
			return value, errors.New("apply-flight-sheet requires a flight sheet")
		}
	default:
		return value, errors.New("unsupported schedule action")
	}
	days, err := normalizeDays(value.Days)
	if err != nil {
		return value, err
	}
	value.Days = days
	value.RigIDs = uniqueStrings(value.RigIDs)
	if len(value.RigIDs) == 0 {
		return value, errors.New("schedule requires at least one worker")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rigID := range value.RigIDs {
		if _, ok := s.state.Rigs[rigID]; !ok {
			return value, fmt.Errorf("worker %s does not exist", rigID)
		}
	}
	if value.FlightSheetID != "" {
		if _, ok := s.state.FlightSheets[value.FlightSheetID]; !ok {
			return value, errors.New("flight sheet does not exist")
		}
	}
	if value.ID == "" {
		value.ID = mustToken(12)
		value.CreatedAt = time.Now().UTC()
	} else if previous, ok := s.state.Schedules[value.ID]; ok {
		value.CreatedAt = previous.CreatedAt
		value.LastRunMinute = previous.LastRunMinute
	} else {
		return value, ErrNotFound
	}
	value.Name = strings.TrimSpace(value.Name)
	s.state.Schedules[value.ID] = value
	s.recordLocked("save", "schedule", value.ID, map[string]any{"name": value.Name, "action": value.Action})
	return value, s.saveLocked()
}

func (s *Store) RunSchedulesAt(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	minute := now.Format("2006-01-02T15:04")
	weekday := int(now.Weekday())
	changed := false
	for id, schedule := range s.state.Schedules {
		if !schedule.Enabled || schedule.Time != now.Format("15:04") || schedule.LastRunMinute == minute || !containsDay(schedule.Days, weekday) {
			continue
		}
		for _, rigID := range schedule.RigIDs {
			rig, ok := s.state.Rigs[rigID]
			if !ok {
				continue
			}
			if schedule.Action == "apply-flight-sheet" {
				rig.Desired.FlightSheetID = schedule.FlightSheetID
				rig.Desired.Revision++
			} else {
				rig.Commands = append(rig.Commands, protocol.Command{
					ID: mustToken(12), Action: schedule.Action,
					CreatedAt: now.UTC(), Status: "pending",
				})
			}
		}
		schedule.LastRunMinute = minute
		s.state.Schedules[id] = schedule
		s.recordLocked("run", "schedule", id, map[string]any{"name": schedule.Name})
		changed = true
	}
	if changed {
		return s.saveLocked()
	}
	return nil
}

func normalizeDays(days []int) ([]int, error) {
	seen := make(map[int]bool)
	result := make([]int, 0, len(days))
	for _, day := range days {
		if day < 0 || day > 6 {
			return nil, errors.New("schedule days must be between 0 and 6")
		}
		if !seen[day] {
			seen[day] = true
			result = append(result, day)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("schedule requires at least one day")
	}
	sort.Ints(result)
	return result, nil
}

func containsDay(days []int, expected int) bool {
	for _, day := range days {
		if day == expected {
			return true
		}
	}
	return false
}
