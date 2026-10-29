package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitLabPipelineSchedules(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.EscapedPath()+" "+string(body))
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pipeline_schedules"):
			w.Write([]byte(`[{"id":3,"description":"stagefreight: nightly","ref":"refs/heads/main","cron":"17 4 * * *","cron_timezone":"UTC","active":false,"owner":{"id":9}}]`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pipeline_schedules/3"):
			w.Write([]byte(`{"id":3,"ref":"main","variables":[{"key":"SF_CI_SCHEDULE","value":"old"}]}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/pipeline_schedules"):
			w.Write([]byte(`{"id":11}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	g := &GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "group/app"}
	ctx := context.Background()

	list, err := g.ListPipelineSchedules(ctx)
	if err != nil || len(list) != 1 || list[0].Ref != "main" || list[0].OwnerID != 9 || list[0].Active {
		t.Fatalf("list = %+v, %v", list, err)
	}
	s, _ := g.GetPipelineSchedule(ctx, 3)
	if s.Variables["SF_CI_SCHEDULE"] != "old" {
		t.Fatalf("variables = %v", s.Variables)
	}
	id, err := g.CreatePipelineSchedule(ctx, PipelineSchedule{Description: "stagefreight: weekly", Ref: "main", Cron: "42 6 * * 1", Timezone: "UTC", Active: true})
	if err != nil || id != 11 {
		t.Fatalf("create = %d, %v", id, err)
	}
	_ = g.SetPipelineScheduleVariable(ctx, 11, "SF_CI_SCHEDULE", "weekly", false)
	_ = g.SetPipelineScheduleVariable(ctx, 3, "SF_CI_SCHEDULE", "nightly", true)
	_ = g.TakePipelineScheduleOwnership(ctx, 3)

	joined := strings.Join(calls, "\n")
	for _, want := range []string{
		"GET /api/v4/projects/group%2Fapp/pipeline_schedules",
		"POST /api/v4/projects/group%2Fapp/pipeline_schedules/11/variables",
		"PUT /api/v4/projects/group%2Fapp/pipeline_schedules/3/variables/SF_CI_SCHEDULE",
		"POST /api/v4/projects/group%2Fapp/pipeline_schedules/3/take_ownership",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("calls missing %q:\n%s", want, joined)
		}
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "POST /api/v4/projects/group%2Fapp/pipeline_schedules {") {
			var body map[string]any
			json.Unmarshal([]byte(c[strings.Index(c, "{"):]), &body)
			if body["ref"] != "refs/heads/main" || body["cron_timezone"] != "UTC" || body["active"] != true {
				t.Errorf("create body = %v", body)
			}
		}
	}
}
