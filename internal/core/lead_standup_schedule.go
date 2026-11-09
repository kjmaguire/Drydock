package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kjmaguire/drydock/internal/tasks"
)

// The team lead's stand-up on a schedule (H11). Off until the person turns it on for a project; then the lead posts its
// stand-up once on each weekday from the chosen hour (the PC's local time), only while the project has active tasks, and
// the message says it was posted by schedule. It is the same paid lead turn as Run stand-up, within the same license and
// budget checks; the person chose it, and turning the schedule off stops it.

const (
	settingStandupSchedule = "lead.standup.schedule:"
	settingStandupLast     = "lead.standup.last:"
	defaultStandupHour     = 9
)

// StandupSchedule is a project's stand-up schedule (lead.standup.schedule.get/set).
type StandupSchedule struct {
	ProjectID string `json:"projectId"`
	Enabled   bool   `json:"enabled"`
	HourLocal int    `json:"hourLocal"` // 0 to 23, the PC's local time
}

func (c *Core) standupSchedule(ctx context.Context, projectID string) StandupSchedule {
	s := StandupSchedule{ProjectID: projectID, HourLocal: defaultStandupHour}
	var raw string
	if c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingStandupSchedule+projectID).Scan(&raw) == nil {
		var v StandupSchedule
		if json.Unmarshal([]byte(raw), &v) == nil && v.HourLocal >= 0 && v.HourLocal <= 23 {
			s.Enabled, s.HourLocal = v.Enabled, v.HourLocal
		}
	}
	return s
}

func (c *Core) setStandupSchedule(ctx context.Context, s StandupSchedule) (StandupSchedule, error) {
	if _, err := c.GetProject(ctx, s.ProjectID); err != nil {
		return StandupSchedule{}, err
	}
	if s.HourLocal < 0 || s.HourLocal > 23 {
		return StandupSchedule{}, errors.New("the stand-up hour is between 0 and 23")
	}
	b, _ := json.Marshal(StandupSchedule{Enabled: s.Enabled, HourLocal: s.HourLocal})
	if err := c.setSetting(ctx, settingStandupSchedule+s.ProjectID, string(b)); err != nil {
		return StandupSchedule{}, err
	}
	c.publish(ctx, "lead.standup_schedule", s.ProjectID, map[string]any{"enabled": s.Enabled, "hourLocal": s.HourLocal})
	return c.standupSchedule(ctx, s.ProjectID), nil
}

// standupDue says whether a scheduled stand-up is due at now: the schedule is on, it is a weekday at or after the hour, and
// none was started today (last is the day of the last one, "2006-01-02").
func standupDue(s StandupSchedule, last string, now time.Time) bool {
	if !s.Enabled {
		return false
	}
	if wd := now.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	return now.Hour() >= s.HourLocal && last != now.Format(time.DateOnly)
}

// runScheduledStandups starts every project's due stand-up through run. A day is used once its turn starts, whatever happens
// (no retry loop of paid turns); a day without active tasks posts nothing.
func (c *Core) runScheduledStandups(ctx context.Context, now time.Time, run func(ctx context.Context, projectID string) error) {
	ps, err := c.Projects(ctx)
	if err != nil {
		return
	}
	for _, p := range ps {
		s := c.standupSchedule(ctx, p.ID)
		var last string
		_ = c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingStandupLast+p.ID).Scan(&last)
		if !standupDue(s, last, now) || c.RequireLicensed(ctx, "lead_standup") != nil {
			continue
		}
		// a day with nothing active says nothing (looked at again on the next beat)
		active, err := c.Tasks.List(ctx, tasks.Filter{ProjectID: p.ID, Statuses: []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress, tasks.Blocked, tasks.WaitingHuman, tasks.Review, tasks.Verification}, Limit: 1})
		if err != nil || len(active) == 0 {
			continue
		}
		if err := c.setSetting(ctx, settingStandupLast+p.ID, now.Format(time.DateOnly)); err != nil {
			continue
		}
		if err := run(ctx, p.ID); err != nil {
			c.logf("scheduled stand-up: %v", err)
		}
	}
}
