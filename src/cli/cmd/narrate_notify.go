package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/scribe"
)

// dispatchNotifications sends each configured notification whose when: matches —
// the one trigger grammar (events/branches/git_tags) extended with the outcomes:
// dimension. subject/body/click are freeform stencil bodies; omitted, they default
// to the shipped subject and the run's ARC body (postmortem on failure, summary
// otherwise). Best-effort: a failed send warns, never fails the phase.
func dispatchNotifications(appCfg *config.Config, rootDir string, st *cistate.State) {
	outcome := outcomeWord(st.PipelineStatus())
	failed := outcome == "failure"

	tagPolicies := make(map[string]string, len(appCfg.Git.Tags))
	for _, ts := range appCfg.Git.Tags {
		tagPolicies[ts.ID] = ts.Pattern
	}

	for _, n := range appCfg.Notifications {
		if r := config.NotificationEligibility(n.When, outcome, config.CIEvent(), config.CIBranch(), config.CITag(), config.CIProvider(), tagPolicies, appCfg.Git.Branches); !r.Eligible {
			continue
		}

		subjectBody := n.Subject
		if subjectBody == "" {
			subjectBody = config.ShippedNotificationSubject
		}
		subject := scribe.RenderText(appCfg, rootDir, subjectBody)

		bodyTmpl := n.Body
		if bodyTmpl == "" {
			if failed {
				bodyTmpl = "{postmortem}"
			} else {
				bodyTmpl = "{summary}"
			}
		}
		body := trimBodyToLength(scribe.RenderText(appCfg, rootDir, bodyTmpl), n.MaxLength)
		if body == "" {
			// A body whose every line elided has nothing to say — a degraded AI
			// stencil, a modality with no matching facts. Skip rather than ping
			// noise (ntfy renders an empty POST as "triggered").
			fmt.Printf("  narrate: notification %q skipped — body rendered empty\n", n.ID)
			continue
		}

		click := n.Click
		if click == "" {
			click = "{pipeline_url}"
		}
		click = scribe.RenderText(appCfg, rootDir, click)

		var err error
		switch n.Provider {
		case "ntfy":
			err = sendNtfy(n, subject, body, click, failed)
		case "webhook":
			err = sendWebhook(n, body)
		default:
			err = fmt.Errorf("unknown provider %q", n.Provider)
		}
		if err != nil {
			fmt.Printf("  narrate: notification %q failed: %v\n", n.ID, err)
		} else {
			fmt.Printf("  narrate: notified %q (%s)\n", n.ID, n.Provider)
		}
	}
}

// outcomeWord maps the pipeline status to the when.outcomes vocabulary.
func outcomeWord(pipelineStatus string) string {
	switch pipelineStatus {
	case "failing":
		return "failure"
	case "warning":
		return "warning"
	case "passing":
		return "success"
	default:
		return "unknown" // matches only an empty outcomes:
	}
}

// trimBodyToLength enforces max_length (bytes) at a LINE boundary, always
// preserving the pipeline-link line ("→ …") so tap-through survives the cut.
func trimBodyToLength(body string, max int) string {
	if max <= 0 || len(body) <= max {
		return body
	}
	lines := strings.Split(body, "\n")
	link := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "→") {
			link = l
		}
	}
	budget := max
	if link != "" {
		budget -= len(link) + 1
	}
	var kept []string
	used := 0
	for _, l := range lines {
		if link != "" && l == link {
			continue // re-appended at the end
		}
		if used+len(l)+1 > budget {
			break
		}
		kept = append(kept, l)
		used += len(l) + 1
	}
	if link != "" {
		kept = append(kept, link)
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n ")
}

func sendNtfy(n config.Notification, subject, body, click string, failed bool) error {
	req, err := http.NewRequest(http.MethodPost, n.URL, strings.NewReader(body))
	if err != nil {
		return err
	}
	if subject != "" {
		req.Header.Set("Title", subject)
	}
	// credentials: NTFY → NTFY_TOKEN (or _PASS/_PASSWORD), sent as a Bearer token.
	if cred := credentials.ResolvePrefix(n.Credentials); cred.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+cred.Secret)
	}

	// Failures cut through by default (high priority + siren); passes are a quiet check.
	priority := n.Priority
	if priority == "" && failed {
		priority = "high"
	}
	if priority != "" {
		req.Header.Set("Priority", priority)
	}
	tags := []string(n.Tags)
	if len(tags) == 0 {
		if failed {
			tags = []string{"rotating_light"}
		} else {
			tags = []string{"white_check_mark"}
		}
	}
	req.Header.Set("Tags", strings.Join(tags, ","))

	if click != "" {
		req.Header.Set("Click", click)
	}
	if n.Attach != "" {
		req.Header.Set("Attach", n.Attach)
	}
	if n.Actions != "" {
		req.Header.Set("Actions", n.Actions)
	}
	if n.Markdown {
		req.Header.Set("Markdown", "yes")
	}
	if n.Email != "" {
		req.Header.Set("Email", n.Email)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned %s", resp.Status)
	}
	return nil
}

func sendWebhook(n config.Notification, body string) error {
	payload := fmt.Sprintf(`{"content":%q}`, body) // slack/discord/matrix all take a `content` field
	req, err := http.NewRequest(http.MethodPost, n.URL, bytes.NewReader([]byte(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}
