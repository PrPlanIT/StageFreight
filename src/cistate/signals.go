package cistate

import "strings"

// SignalsKey is the Results key under which any subsystem records the signals it raised, as a
// comma-separated list (the when.signals vocabulary).
const SignalsKey = "signals"

// Signals returns every signal the run raised, across all subsystems, in first-seen order.
func (st *State) Signals() []string {
	if st == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, sub := range st.Subsystems {
		for _, sig := range strings.Split(sub.Results[SignalsKey], ",") {
			if sig = strings.TrimSpace(sig); sig != "" && !seen[sig] {
				seen[sig] = true
				out = append(out, sig)
			}
		}
	}
	return out
}
