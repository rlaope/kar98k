package controller

import (
	"time"

	"github.com/kar98k/internal/config"
)

// Scheduler provides time-of-day based TPS multipliers.
type Scheduler struct {
	schedule []config.ScheduleEntry
}

// NewScheduler creates a new scheduler with the given schedule.
func NewScheduler(schedule []config.ScheduleEntry) *Scheduler {
	return &Scheduler{
		schedule: schedule,
	}
}

// GetMultiplier returns the TPS multiplier for the current hour.
func (s *Scheduler) GetMultiplier() float64 {
	if len(s.schedule) == 0 {
		return 1.0
	}

	currentHour := time.Now().Hour()

	// Check entries in reverse order so later entries take precedence
	for i := len(s.schedule) - 1; i >= 0; i-- {
		entry := s.schedule[i]
		for _, hour := range entry.Hours {
			if hour == currentHour {
				return entry.TPSMultiplier
			}
		}
	}

	return 1.0
}

// GetMultiplierForHour returns the TPS multiplier for a specific hour.
func (s *Scheduler) GetMultiplierForHour(hour int) float64 {
	if len(s.schedule) == 0 {
		return 1.0
	}

	// Normalize hour to 0-23
	hour = ((hour % 24) + 24) % 24

	for i := len(s.schedule) - 1; i >= 0; i-- {
		entry := s.schedule[i]
		for _, h := range entry.Hours {
			if h == hour {
				return entry.TPSMultiplier
			}
		}
	}

	return 1.0
}

// GetScheduleInfo returns information about the current schedule.
type ScheduleInfo struct {
	CurrentHour       int
	CurrentMultiplier float64
	NextChangeHour    int
	NextMultiplier    float64
}

// GetInfo returns current schedule information.
func (s *Scheduler) GetInfo() ScheduleInfo {
	currentHour := time.Now().Hour()
	currentMult := s.GetMultiplierForHour(currentHour)

	// Find next hour with different multiplier
	nextChangeHour := -1
	nextMult := currentMult

	for i := 1; i <= 24; i++ {
		testHour := (currentHour + i) % 24
		testMult := s.GetMultiplierForHour(testHour)
		if testMult != currentMult {
			nextChangeHour = testHour
			nextMult = testMult
			break
		}
	}

	return ScheduleInfo{
		CurrentHour:       currentHour,
		CurrentMultiplier: currentMult,
		NextChangeHour:    nextChangeHour,
		NextMultiplier:    nextMult,
	}
}

// GetAllMultipliers returns multipliers for all 24 hours.
func (s *Scheduler) GetAllMultipliers() [24]float64 {
	var multipliers [24]float64
	for h := 0; h < 24; h++ {
		multipliers[h] = s.GetMultiplierForHour(h)
	}
	return multipliers
}
