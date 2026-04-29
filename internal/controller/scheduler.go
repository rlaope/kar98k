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
	return s.GetMultiplierForHour(time.Now().Hour())
}

// GetMultiplierForHour returns the TPS multiplier for a specific hour.
//
// Selection rule: among entries whose hours include the target hour,
// the one with the highest Priority wins. Ties fall back to "later
// entry wins" — preserving the historical position-based behaviour
// for schedules that don't set Priority.
func (s *Scheduler) GetMultiplierForHour(hour int) float64 {
	if len(s.schedule) == 0 {
		return 1.0
	}

	// Normalize hour to 0-23
	hour = ((hour % 24) + 24) % 24

	winnerIdx := -1
	for i := range s.schedule {
		entry := s.schedule[i]
		matches := false
		for _, h := range entry.Hours {
			if h == hour {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		// Higher priority wins; equal priority falls back to "later wins"
		// because the iteration is left-to-right.
		if winnerIdx < 0 || entry.Priority >= s.schedule[winnerIdx].Priority {
			winnerIdx = i
		}
	}

	if winnerIdx < 0 {
		return 1.0
	}
	return s.schedule[winnerIdx].TPSMultiplier
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
