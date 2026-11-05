package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/skills"
)

// Repository skills are found without a button (effort review E5): the project's own .aet/skills is read when the project gets
// its repository (create with a folder, attach, setup), after Sync now fast-forwards the folder, and when the window gets focus
// again with the project open (skill.refresh, at most every 30 seconds). A scan only reads the main checkout and offers: a
// committed, unmodified skill that passes the scan is accepted as before (skills.ScanRepo), anything else waits for the person.
// Each scan publishes skill.scanned (counts only). The skills that wait for the person are told once, in #alerts with a Needs-you
// item (the notice skillwatch posts for registry updates): a skill is told once, and again only when its content changes.

// EventSkillScanned is published after every repository scan, with its counts.
const EventSkillScanned = "skill.scanned"

// ScanRepoSkills scans the project's repository for skills now, publishes skill.scanned and tells the person about skills that
// wait for a decision. A project without Knowledge or without a repository scans nothing.
func (c *Core) ScanRepoSkills(ctx context.Context, projectID string) (skills.ScanResult, error) {
	k := c.Knowledge()
	if k == nil {
		return skills.ScanResult{Errors: []string{}}, nil
	}
	k.scanMu.Lock()
	k.scanned[projectID] = time.Now()
	k.scanMu.Unlock()
	res, err := k.Skills.ScanRepo(ctx, projectID)
	if err != nil {
		return res, err
	}
	c.publish(ctx, EventSkillScanned, projectID, map[string]any{"found": res.Found, "accepted": res.Accepted, "offered": res.Offered, "blocked": res.Blocked, "removed": res.Removed})
	c.tellSkillOffers(ctx, projectID)
	return res, nil
}

// scanRepoSkillsQuietly is ScanRepoSkills where a scan is a side effect (a repository attached, a folder synced): a failure is
// logged, never the caller's error.
func (c *Core) scanRepoSkillsQuietly(ctx context.Context, projectID string) {
	if _, err := c.ScanRepoSkills(ctx, projectID); err != nil {
		c.logf("skills scan: %v", err)
	}
}

// refreshRepoSkills is skill.refresh (the window got focus with the project open): a scan when the last is 30 seconds old.
func (c *Core) refreshRepoSkills(ctx context.Context, projectID string) (bool, error) {
	k := c.Knowledge()
	if k == nil {
		return false, nil
	}
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return false, err
	}
	k.scanMu.Lock()
	recent := time.Since(k.scanned[projectID]) < 30*time.Second
	k.scanMu.Unlock()
	if recent {
		return false, nil
	}
	_, err := c.ScanRepoSkills(ctx, projectID)
	return err == nil, err
}

// tellSkillOffers posts, in the project's #alerts with a Needs-you item, the repository skills that wait for the person and
// were not told before. The pairs of name and content already told are kept in settings (skillToldKey), so deciding on one of
// several waiting skills does not tell the others again; a new skill, or a told one whose content changed, is told once.
func (c *Core) tellSkillOffers(ctx context.Context, projectID string) {
	k := c.Knowledge()
	list, err := k.Skills.List(ctx, projectID, skills.StatusOffered, skills.StatusBlocked)
	if err != nil {
		return
	}
	told := c.toldSkills(ctx, projectID)
	var fresh []skills.Skill
	for _, s := range list {
		if s.Origin == skills.OriginRepo && !told[s.Name+":"+s.ContentHash] {
			fresh = append(fresh, s)
		}
	}
	if len(fresh) == 0 {
		return
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Name < fresh[j].Name })
	h := sha256.New()
	names := make([]string, 0, len(fresh))
	for _, s := range fresh {
		fmt.Fprintf(h, "%s:%s\n", s.Name, s.ContentHash)
		names = append(names, s.Name)
		told[s.Name+":"+s.ContentHash] = true
	}
	key := "skillscan:" + projectID + ":" + hex.EncodeToString(h.Sum(nil))[:16]
	c.alertsNotice(ctx, projectID, key, skillOfferNotice(names), true)
	pairs := make([]string, 0, len(told))
	for p := range told {
		pairs = append(pairs, p)
	}
	sort.Strings(pairs)
	if err := c.setSetting(ctx, skillToldKey+projectID, strings.Join(pairs, "\n")); err != nil {
		c.logf("skills told: %v", err)
	}
}

// skillToldKey + project id is the setting that holds the repository skills already told, one "name:content hash" a line.
const skillToldKey = "skillscan.told:"

func (c *Core) toldSkills(ctx context.Context, projectID string) map[string]bool {
	told := map[string]bool{}
	var v string
	if c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, skillToldKey+projectID).Scan(&v) != nil {
		return told
	}
	for _, line := range strings.Split(v, "\n") {
		if line != "" {
			told[line] = true
		}
	}
	return told
}

// skillOfferNotice is the alerts message: its first sentence is the Needs-you item's title.
func skillOfferNotice(names []string) string {
	n := len(names)
	shown := names
	more := ""
	if n > 5 {
		shown, more = names[:5], fmt.Sprintf(" and %d more", n-5)
	}
	return fmt.Sprintf("%d %s in the repository %s for your decision. %s%s: open Skills to accept or reject %s; agents get %s only once you accept.",
		n, plural(n, "skill", "skills"), plural(n, "waits", "wait"), strings.Join(shown, ", "), more, plural(n, "it", "them"), plural(n, "it", "them"))
}
