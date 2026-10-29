package forge

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// PipelineSchedule is one forge-side pipeline schedule.
type PipelineSchedule struct {
	ID          int
	Description string
	Ref         string // branch the schedule runs on (no refs/heads/ prefix)
	Cron        string
	Timezone    string
	Active      bool
	OwnerID     int
	Variables   map[string]string // filled by GetPipelineSchedule only
}

// PipelineScheduler is the OPTIONAL capability interface a forge implements when its
// pipeline schedules are API state rather than CI-file content (GitLab). Forges whose
// schedules live in the rendered CI file (Actions, Azure) do not implement it.
type PipelineScheduler interface {
	// CurrentUserID is the id of the user the client's token acts as.
	CurrentUserID(ctx context.Context) (int, error)
	ListPipelineSchedules(ctx context.Context) ([]PipelineSchedule, error)
	GetPipelineSchedule(ctx context.Context, id int) (PipelineSchedule, error)
	CreatePipelineSchedule(ctx context.Context, s PipelineSchedule) (int, error)
	UpdatePipelineSchedule(ctx context.Context, s PipelineSchedule) error
	DeletePipelineSchedule(ctx context.Context, id int) error
	// TakePipelineScheduleOwnership makes the token's user the owner (the identity a
	// schedule's pipelines run as).
	TakePipelineScheduleOwnership(ctx context.Context, id int) error
	// SetPipelineScheduleVariable creates the variable, or updates it when exists is true.
	SetPipelineScheduleVariable(ctx context.Context, id int, key, value string, exists bool) error
}

type gitlabSchedule struct {
	ID           int    `json:"id"`
	Description  string `json:"description"`
	Ref          string `json:"ref"`
	Cron         string `json:"cron"`
	CronTimezone string `json:"cron_timezone"`
	Active       bool   `json:"active"`
	Owner        *struct {
		ID int `json:"id"`
	} `json:"owner"`
	Variables []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"variables"`
}

func (s gitlabSchedule) toSchedule() PipelineSchedule {
	out := PipelineSchedule{ID: s.ID, Description: s.Description, Ref: strings.TrimPrefix(s.Ref, "refs/heads/"), Cron: s.Cron, Timezone: s.CronTimezone, Active: s.Active}
	if s.Owner != nil {
		out.OwnerID = s.Owner.ID
	}
	if s.Variables != nil {
		out.Variables = map[string]string{}
		for _, v := range s.Variables {
			out.Variables[v.Key] = v.Value
		}
	}
	return out
}

// gitlabRef is the full ref GitLab stores for a branch schedule.
func gitlabRef(branch string) string { return "refs/heads/" + branch }

func (g *GitLabForge) CurrentUserID(ctx context.Context) (int, error) {
	var u struct {
		ID int `json:"id"`
	}
	if err := g.doJSON(ctx, "GET", g.BaseURL+"/api/v4/user", nil, &u); err != nil {
		return 0, fmt.Errorf("gitlab: current user: %w", err)
	}
	return u.ID, nil
}

func (g *GitLabForge) ListPipelineSchedules(ctx context.Context) ([]PipelineSchedule, error) {
	var raw []gitlabSchedule
	if err := g.doJSON(ctx, "GET", g.apiURL("/pipeline_schedules?per_page=100"), nil, &raw); err != nil {
		return nil, fmt.Errorf("gitlab: list pipeline schedules: %w", err)
	}
	out := make([]PipelineSchedule, 0, len(raw))
	for _, s := range raw {
		out = append(out, s.toSchedule())
	}
	return out, nil
}

func (g *GitLabForge) GetPipelineSchedule(ctx context.Context, id int) (PipelineSchedule, error) {
	var raw gitlabSchedule
	if err := g.doJSON(ctx, "GET", g.apiURL(fmt.Sprintf("/pipeline_schedules/%d", id)), nil, &raw); err != nil {
		return PipelineSchedule{}, fmt.Errorf("gitlab: get pipeline schedule %d: %w", id, err)
	}
	return raw.toSchedule(), nil
}

func scheduleBody(s PipelineSchedule) map[string]any {
	return map[string]any{
		"description":   s.Description,
		"ref":           gitlabRef(s.Ref),
		"cron":          s.Cron,
		"cron_timezone": s.Timezone,
		"active":        s.Active,
	}
}

func (g *GitLabForge) CreatePipelineSchedule(ctx context.Context, s PipelineSchedule) (int, error) {
	var raw gitlabSchedule
	if err := g.doJSON(ctx, "POST", g.apiURL("/pipeline_schedules"), scheduleBody(s), &raw); err != nil {
		return 0, fmt.Errorf("gitlab: create pipeline schedule: %w", err)
	}
	return raw.ID, nil
}

func (g *GitLabForge) UpdatePipelineSchedule(ctx context.Context, s PipelineSchedule) error {
	if err := g.doJSON(ctx, "PUT", g.apiURL(fmt.Sprintf("/pipeline_schedules/%d", s.ID)), scheduleBody(s), nil); err != nil {
		return fmt.Errorf("gitlab: update pipeline schedule %d: %w", s.ID, err)
	}
	return nil
}

func (g *GitLabForge) DeletePipelineSchedule(ctx context.Context, id int) error {
	if err := g.doJSON(ctx, "DELETE", g.apiURL(fmt.Sprintf("/pipeline_schedules/%d", id)), nil, nil); err != nil {
		return fmt.Errorf("gitlab: delete pipeline schedule %d: %w", id, err)
	}
	return nil
}

func (g *GitLabForge) TakePipelineScheduleOwnership(ctx context.Context, id int) error {
	if err := g.doJSON(ctx, "POST", g.apiURL(fmt.Sprintf("/pipeline_schedules/%d/take_ownership", id)), nil, nil); err != nil {
		return fmt.Errorf("gitlab: take ownership of pipeline schedule %d: %w", id, err)
	}
	return nil
}

func (g *GitLabForge) SetPipelineScheduleVariable(ctx context.Context, id int, key, value string, exists bool) error {
	method, path := "POST", fmt.Sprintf("/pipeline_schedules/%d/variables", id)
	body := map[string]any{"key": key, "value": value}
	if exists {
		method, path = "PUT", fmt.Sprintf("/pipeline_schedules/%d/variables/%s", id, url.PathEscape(key))
		body = map[string]any{"value": value}
	}
	if err := g.doJSON(ctx, method, g.apiURL(path), body, nil); err != nil {
		return fmt.Errorf("gitlab: set variable %s on pipeline schedule %d: %w", key, id, err)
	}
	return nil
}
