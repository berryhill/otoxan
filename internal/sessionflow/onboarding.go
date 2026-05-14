package sessionflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/silas/otoxan/internal/firstrun"
)

// onboardingFlow is the real multi-step first-run experience.
// Steps: hello → confirm_context → pick_intent → minimal_setup → handoff.
// Everything detectable is gathered silently; the user only confirms or fills gaps.
type onboardingFlow struct{}

// stepState is the JSON shape carried in RouteInput.StepState / RouteOutput.NextStepState.
type stepState struct {
	Step     string            `json:"step"`
	Home     string            `json:"home"`
	Detected detected        `json:"detected"`
	Confirmed map[string]bool  `json:"confirmed"`
	Intent   string            `json:"intent"`
	Setup    map[string]string `json:"setup"`
}

func (onboardingFlow) Route(_ context.Context, in RouteInput) (RouteOutput, error) {
	// Parse incoming step state, or start fresh.
	var st stepState
	if len(in.StepState) > 0 {
		if err := json.Unmarshal(in.StepState, &st); err != nil {
			return RouteOutput{
				Action: "error",
				Error:  fmt.Sprintf("onboarding: corrupt step state: %v", err),
			}, nil
		}
	}

	// Ensure Home is populated from RouteInput if missing in state.
	if st.Home == "" {
		st.Home = in.Home
	}

	// Determine current step.
	step := st.Step
	if step == "" {
		step = "hello"
	}

	switch step {
	case "hello":
		return onboardingFlow{}.hello(st)
	case "confirm_context":
		return onboardingFlow{}.confirmContext(in.UserMessage, st)
	case "pick_intent":
		return onboardingFlow{}.pickIntent(in.UserMessage, st)
	case "minimal_setup":
		return onboardingFlow{}.minimalSetup(in.UserMessage, st)
	case "handoff":
		return onboardingFlow{}.handoff(st)
	default:
		return RouteOutput{
			Action: "error",
			Error:  fmt.Sprintf("onboarding: unknown step %q", step),
		}, nil
	}
}

// ------------------------------------------------------------------
// hello
// ------------------------------------------------------------------

func (onboardingFlow) hello(st stepState) (RouteOutput, error) {
	// On the very first turn we have no user message yet; the dispatch loop
	// will call Route with an empty UserMessage.  We reply with a greeting
	// and advance to confirm_context.
	st.Step = "confirm_context"

	payload := `Welcome to otoxan — your AI agent operations CLI.

I'll set things up by detecting what I can from your environment, then ask you to confirm or fill any gaps.

Press Enter to continue, or type "skip" to jump straight to the default flow.`

	nextState, err := json.Marshal(st)
	if err != nil {
		return RouteOutput{Action: "error", Error: err.Error()}, nil
	}

	return RouteOutput{
		Action:      "model_complete",
		Payload:     payload,
		NextStepID:  "confirm_context",
		NextStepState: nextState,
	}, nil
}

// ------------------------------------------------------------------
// confirm_context
// ------------------------------------------------------------------

func (onboardingFlow) confirmContext(msg string, st stepState) (RouteOutput, error) {
	// If the user typed "skip", jump straight to handoff.
	if strings.TrimSpace(strings.ToLower(msg)) == "skip" {
		st.Step = "handoff"
		nextState, _ := json.Marshal(st)
		return RouteOutput{
			Action:       "model_complete",
			Payload:      "Skipping onboarding. Handing off to the default flow...",
			NextStepID:   "handoff",
			NextStepState: nextState,
		}, nil
	}

	// First time in this step: present detected context and ask for confirmation.
	if st.Detected.CWD == "" {
		st.Detected = detectContext(st.Home)
	}

	if st.Confirmed == nil {
		st.Confirmed = make(map[string]bool)
	}

	// If we haven't presented the summary yet, do it now.
	if !st.Confirmed["presented"] {
		st.Confirmed["presented"] = true
		st.Step = "confirm_context"

		summary := st.Detected.summary()
		payload := fmt.Sprintf(`Here's what I detected from your environment:

%s

Is this correct? (yes/no) — if anything looks wrong, type "no" and I'll ask you to confirm each item.`, summary)

		nextState, err := json.Marshal(st)
		if err != nil {
			return RouteOutput{Action: "error", Error: err.Error()}, nil
		}

		return RouteOutput{
			Action:        "model_complete",
			Payload:       payload,
			NextStepID:    "confirm_context",
			NextStepState: nextState,
		}, nil
	}

	// We have presented the summary; now we expect a yes/no response.
	answer := strings.TrimSpace(strings.ToLower(msg))
	switch answer {
	case "yes", "y", "":
		st.Confirmed["context"] = true
		st.Step = "pick_intent"
		nextState, err := json.Marshal(st)
		if err != nil {
			return RouteOutput{Action: "error", Error: err.Error()}, nil
		}
		payload := "Great! What would you like to do first?\n\n1. Start a new project\n2. Work on an existing repo\n3. Explore otoxan features\n4. Just chat\n\nType the number or describe your intent."
		return RouteOutput{
			Action:        "model_complete",
			Payload:       payload,
			NextStepID:    "pick_intent",
			NextStepState: nextState,
		}, nil
	case "no", "n":
		// User wants to correct context. For v1 we just present a message
		// and move on; detailed per-field correction is deferred.
		st.Confirmed["context"] = false
		st.Step = "pick_intent"
		nextState, err := json.Marshal(st)
		if err != nil {
			return RouteOutput{Action: "error", Error: err.Error()}, nil
		}
		payload := "No problem — you can adjust your profile later with `otoxan init`.\n\nWhat would you like to do first?\n\n1. Start a new project\n2. Work on an existing repo\n3. Explore otoxan features\n4. Just chat\n\nType the number or describe your intent."
		return RouteOutput{
			Action:        "model_complete",
			Payload:       payload,
			NextStepID:    "pick_intent",
			NextStepState: nextState,
		}, nil
	default:
		// Unrecognised input — stay in confirm_context and re-ask.
		st.Step = "confirm_context"
		nextState, err := json.Marshal(st)
		if err != nil {
			return RouteOutput{Action: "error", Error: err.Error()}, nil
		}
		payload := fmt.Sprintf(`I didn't understand %q. Please answer "yes" or "no".`, msg)
		return RouteOutput{
			Action:        "model_complete",
			Payload:       payload,
			NextStepID:    "confirm_context",
			NextStepState: nextState,
		}, nil
	}
}

// ------------------------------------------------------------------
// pick_intent
// ------------------------------------------------------------------

// buildIntentMenu returns a context-aware list of intent choices.
// The menu always lists 4 primary intents, adapting labels based on
// detected context (e.g. inside a git repo, already has a profile).
func buildIntentMenu(d detected) []string {
	var menu []string
	if d.GitRemote != "" {
		menu = append(menu, "1. Work on this repo")
	} else {
		menu = append(menu, "1. Start a new project")
	}
	if d.GitRemote != "" {
		menu = append(menu, "2. Start a new project")
	} else {
		menu = append(menu, "2. Work on an existing repo")
	}
	menu = append(menu, "3. Explore otoxan features")
	menu = append(menu, "4. Just chat")
	return menu
}

// mapIntentChoice converts the user's raw input (number or text) into a canonical
// intent key, taking the current detected context into account.
func mapIntentChoice(input string, d detected) string {
	choice := strings.TrimSpace(strings.ToLower(input))

	hasRepo := d.GitRemote != ""

	switch choice {
	case "1":
		if hasRepo {
			return "existing_repo"
		}
		return "new_project"
	case "2":
		if hasRepo {
			return "new_project"
		}
		return "existing_repo"
	case "3":
		return "explore"
	case "4":
		return "chat"
	case "work on this repo", "repo", "existing", "existing repo":
		return "existing_repo"
	case "start a new project", "new project", "new":
		return "new_project"
	case "explore otoxan features", "explore", "features":
		return "explore"
	case "just chat", "chat":
		return "chat"
	default:
		return choice
	}
}

func (onboardingFlow) pickIntent(msg string, st stepState) (RouteOutput, error) {
	if st.Setup == nil {
		st.Setup = make(map[string]string)
	}

	// On the first entry into this step (empty msg), present the menu.
	if strings.TrimSpace(msg) == "" {
		menu := buildIntentMenu(st.Detected)
		st.Step = "pick_intent"
		nextState, err := json.Marshal(st)
		if err != nil {
			return RouteOutput{Action: "error", Error: err.Error()}, nil
		}
		payload := fmt.Sprintf("What would you like to do first?\n\n%s\n\nType the number or describe your intent.", strings.Join(menu, "\n"))
		return RouteOutput{
			Action:        "model_complete",
			Payload:       payload,
			NextStepID:    "pick_intent",
			NextStepState: nextState,
		}, nil
	}

	intent := mapIntentChoice(msg, st.Detected)
	st.Intent = intent
	st.Setup["intent"] = intent

	st.Step = "minimal_setup"
	nextState, err := json.Marshal(st)
	if err != nil {
		return RouteOutput{Action: "error", Error: err.Error()}, nil
	}

	payload := fmt.Sprintf(`Got it — "%s".

One last thing: would you like me to create a default agent profile now? (yes/no)

A profile lets otoxan remember your preferences across sessions. You can always create one later with "otoxan init".`, st.Setup["intent"])

	return RouteOutput{
		Action:        "model_complete",
		Payload:       payload,
		NextStepID:    "minimal_setup",
		NextStepState: nextState,
	}, nil
}

// ------------------------------------------------------------------
// minimal_setup
// ------------------------------------------------------------------

func (onboardingFlow) minimalSetup(msg string, st stepState) (RouteOutput, error) {
	if st.Setup == nil {
		st.Setup = make(map[string]string)
	}

	// If the user already has an agent profile, skip all questions and go
	// straight to handoff — nothing to set up.
	if st.Detected.AgentProfileExists {
		st.Setup["create_profile"] = "skipped"
		st.Step = "handoff"
		nextState, err := json.Marshal(st)
		if err != nil {
			return RouteOutput{Action: "error", Error: err.Error()}, nil
		}
		return RouteOutput{
			Action:        "model_complete",
			Payload:       "Profile already configured. All set! Handing you off to the default flow now.",
			NextStepID:    "handoff",
			NextStepState: nextState,
		}, nil
	}

	answer := strings.TrimSpace(strings.ToLower(msg))
	switch answer {
	case "yes", "y":
		st.Setup["create_profile"] = "yes"
	case "no", "n":
		st.Setup["create_profile"] = "no"
	default:
		st.Setup["create_profile"] = "skipped"
	}

	st.Step = "handoff"
	nextState, err := json.Marshal(st)
	if err != nil {
		return RouteOutput{Action: "error", Error: err.Error()}, nil
	}

	payload := "All set! Handing you off to the default flow now."
	return RouteOutput{
		Action:        "model_complete",
		Payload:       payload,
		NextStepID:    "handoff",
		NextStepState: nextState,
	}, nil
}

// ------------------------------------------------------------------
// handoff
// ------------------------------------------------------------------

func (onboardingFlow) handoff(st stepState) (RouteOutput, error) {
	// Mark onboarding complete so future runs skip this flow.
	if st.Home != "" {
		_ = firstrun.MarkOnboardingComplete(st.Home)
	}

	// Return the handoff action so the dispatch session swaps to "default".
	return RouteOutput{
		Action:  "handoff",
		Payload: "default",
	}, nil
}

// HandoffArgs returns the target flow ID extracted from a handoff RouteOutput.
// It is a convenience helper for dispatch session code.
func HandoffArgs(out RouteOutput) (to string, ok bool) {
	if out.Action != "handoff" {
		return "", false
	}
	return out.Payload, true
}

func init() {
	DefaultRegistry.Register("onboarding", onboardingFlow{})
}
