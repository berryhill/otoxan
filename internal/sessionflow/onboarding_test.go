package sessionflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silas/otoxan/internal/firstrun"
)

func TestOnboardingFlow_hello(t *testing.T) {
	flow := onboardingFlow{}
	out, err := flow.Route(context.Background(), RouteInput{})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if !strings.Contains(out.Payload, "Welcome to otoxan") {
		t.Errorf("Payload missing welcome text: %q", out.Payload)
	}
	if out.NextStepID != "confirm_context" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "confirm_context")
	}

	var st stepState
	if err := json.Unmarshal(out.NextStepState, &st); err != nil {
		t.Fatalf("unmarshal step state: %v", err)
	}
	if st.Step != "confirm_context" {
		t.Errorf("step state Step = %q, want %q", st.Step, "confirm_context")
	}
}

func TestOnboardingFlow_confirmContext_skip(t *testing.T) {
	flow := onboardingFlow{}
	st := stepState{Step: "confirm_context", Home: t.TempDir()}
	b, _ := json.Marshal(st)

	out, err := flow.Route(context.Background(), RouteInput{
		StepID:      "confirm_context",
		StepState:   b,
		UserMessage: "skip",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if out.NextStepID != "handoff" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "handoff")
	}
}

func TestOnboardingFlow_confirmContext_presentAndConfirm(t *testing.T) {
	flow := onboardingFlow{}
	st := stepState{Step: "confirm_context", Home: t.TempDir()}
	b, _ := json.Marshal(st)

	// First call: presents detected context.
	out, err := flow.Route(context.Background(), RouteInput{
		StepID:    "confirm_context",
		StepState: b,
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if !strings.Contains(out.Payload, "Working directory:") {
		t.Errorf("Payload missing detected context: %q", out.Payload)
	}

	// Second call: user says "yes".
	out, err = flow.Route(context.Background(), RouteInput{
		StepID:      "confirm_context",
		StepState:   out.NextStepState,
		UserMessage: "yes",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if out.NextStepID != "pick_intent" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "pick_intent")
	}
}

func TestOnboarding_PickIntent(t *testing.T) {
	flow := onboardingFlow{}
	st := stepState{
		Step: "pick_intent",
		Home: t.TempDir(),
		Detected: detected{
			CWD:                "/home/silas/code/otoxan",
			GitRemote:          "https://github.com/silas/otoxan.git",
			OS:                 "linux",
			Shell:              "zsh",
			Username:           "silas",
			Editor:             "vim",
			AgentProfileExists: true,
		},
	}
	b, _ := json.Marshal(st)

	// First call: presents the menu.
	out, err := flow.Route(context.Background(), RouteInput{
		StepID:    "pick_intent",
		StepState: b,
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if out.NextStepID != "pick_intent" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "pick_intent")
	}

	// Menu should list 4 intents when GitRemote is set and profile exists.
	menu := out.Payload
	// The payload has header lines plus 4 menu items; just verify all 4 are present.
	for _, want := range []string{"1. Work on this repo", "2. Start a new project", "3. Explore otoxan features", "4. Just chat"} {
		if !strings.Contains(menu, want) {
			t.Errorf("menu missing %q\nfull:\n%s", want, menu)
		}
	}

	// Second call: user selects option 1.
	out, err = flow.Route(context.Background(), RouteInput{
		StepID:      "pick_intent",
		StepState:   out.NextStepState,
		UserMessage: "1",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.NextStepID != "minimal_setup" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "minimal_setup")
	}

	var next stepState
	if err := json.Unmarshal(out.NextStepState, &next); err != nil {
		t.Fatalf("unmarshal step state: %v", err)
	}
	if next.Setup["intent"] != "existing_repo" {
		t.Errorf("intent = %q, want %q", next.Setup["intent"], "existing_repo")
	}
}

func TestOnboarding_MinimalSetup(t *testing.T) {
	flow := onboardingFlow{}
	st := stepState{
		Step: "minimal_setup",
		Home: t.TempDir(),
		Detected: detected{
			AgentProfileExists: true,
		},
	}
	b, _ := json.Marshal(st)

	// With AgentProfileExists=true, minimal_setup should short-circuit to handoff
	// without asking any question, even with an empty user message.
	out, err := flow.Route(context.Background(), RouteInput{
		StepID:      "minimal_setup",
		StepState:   b,
		UserMessage: "",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if out.NextStepID != "handoff" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "handoff")
	}
	if !strings.Contains(out.Payload, "Profile already configured") {
		t.Errorf("Payload missing short-circuit message: %q", out.Payload)
	}

	var next stepState
	if err := json.Unmarshal(out.NextStepState, &next); err != nil {
		t.Fatalf("unmarshal step state: %v", err)
	}
	if next.Setup["create_profile"] != "skipped" {
		t.Errorf("create_profile = %q, want %q", next.Setup["create_profile"], "skipped")
	}
}

func TestOnboardingFlow_pickIntent(t *testing.T) {
	flow := onboardingFlow{}
	st := stepState{Step: "pick_intent", Home: t.TempDir()}
	b, _ := json.Marshal(st)

	out, err := flow.Route(context.Background(), RouteInput{
		StepID:      "pick_intent",
		StepState:   b,
		UserMessage: "2",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if out.NextStepID != "minimal_setup" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "minimal_setup")
	}

	var next stepState
	if err := json.Unmarshal(out.NextStepState, &next); err != nil {
		t.Fatalf("unmarshal step state: %v", err)
	}
	if next.Setup["intent"] != "existing_repo" {
		t.Errorf("intent = %q, want %q", next.Setup["intent"], "existing_repo")
	}
}

func TestOnboardingFlow_minimalSetup(t *testing.T) {
	flow := onboardingFlow{}
	st := stepState{Step: "minimal_setup", Home: t.TempDir()}
	b, _ := json.Marshal(st)

	out, err := flow.Route(context.Background(), RouteInput{
		StepID:      "minimal_setup",
		StepState:   b,
		UserMessage: "no",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if out.NextStepID != "handoff" {
		t.Errorf("NextStepID = %q, want %q", out.NextStepID, "handoff")
	}
}

func TestOnboarding_Handoff(t *testing.T) {
	home := t.TempDir()
	flow := onboardingFlow{}
	st := stepState{Step: "handoff", Home: home}
	b, _ := json.Marshal(st)

	out, err := flow.Route(context.Background(), RouteInput{
		StepID:    "handoff",
		StepState: b,
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "handoff" {
		t.Errorf("Action = %q, want %q", out.Action, "handoff")
	}

	to, ok := HandoffArgs(out)
	if !ok {
		t.Fatal("HandoffArgs returned ok=false, want true")
	}
	if to != "default" {
		t.Errorf("HandoffArgs to = %q, want %q", to, "default")
	}

	// Verify sentinel was written.
	first, err := firstrun.IsFirstRun(home)
	if err != nil {
		t.Fatalf("IsFirstRun error: %v", err)
	}
	if first {
		t.Error("expected IsFirstRun=false after handoff, got true")
	}

	// Verify the sentinel file exists directly.
	path := filepath.Join(home, ".onboarded")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected .onboarded to exist: %v", err)
	}
}

func TestOnboardingFlow_handoff(t *testing.T) {
	home := t.TempDir()
	flow := onboardingFlow{}
	st := stepState{Step: "handoff", Home: home}
	b, _ := json.Marshal(st)

	out, err := flow.Route(context.Background(), RouteInput{
		StepID:    "handoff",
		StepState: b,
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "handoff" {
		t.Errorf("Action = %q, want %q", out.Action, "handoff")
	}
	if out.Payload != "default" {
		t.Errorf("Payload = %q, want %q", out.Payload, "default")
	}

	// Verify sentinel was written.
	first, err := firstrun.IsFirstRun(home)
	if err != nil {
		t.Fatalf("IsFirstRun error: %v", err)
	}
	if first {
		t.Error("expected IsFirstRun=false after handoff, got true")
	}
}

func TestOnboardingFlow_fullWalkthrough(t *testing.T) {
	home := t.TempDir()
	flow := onboardingFlow{}

	// 1. hello
	out, err := flow.Route(context.Background(), RouteInput{Home: home})
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	if out.NextStepID != "confirm_context" {
		t.Fatalf("expected confirm_context, got %q", out.NextStepID)
	}

	// 2. confirm_context (present)
	out, err = flow.Route(context.Background(), RouteInput{
		StepID:    out.NextStepID,
		StepState: out.NextStepState,
		Home:      home,
	})
	if err != nil {
		t.Fatalf("confirm_context present: %v", err)
	}

	// 3. confirm_context (yes)
	out, err = flow.Route(context.Background(), RouteInput{
		StepID:      out.NextStepID,
		StepState:   out.NextStepState,
		UserMessage: "yes",
		Home:        home,
	})
	if err != nil {
		t.Fatalf("confirm_context yes: %v", err)
	}
	if out.NextStepID != "pick_intent" {
		t.Fatalf("expected pick_intent, got %q", out.NextStepID)
	}

	// 4. pick_intent
	out, err = flow.Route(context.Background(), RouteInput{
		StepID:      out.NextStepID,
		StepState:   out.NextStepState,
		UserMessage: "explore",
		Home:        home,
	})
	if err != nil {
		t.Fatalf("pick_intent: %v", err)
	}
	if out.NextStepID != "minimal_setup" {
		t.Fatalf("expected minimal_setup, got %q", out.NextStepID)
	}

	// 5. minimal_setup
	out, err = flow.Route(context.Background(), RouteInput{
		StepID:      out.NextStepID,
		StepState:   out.NextStepState,
		UserMessage: "yes",
		Home:        home,
	})
	if err != nil {
		t.Fatalf("minimal_setup: %v", err)
	}
	if out.NextStepID != "handoff" {
		t.Fatalf("expected handoff, got %q", out.NextStepID)
	}

	// 6. handoff
	out, err = flow.Route(context.Background(), RouteInput{
		StepID:    out.NextStepID,
		StepState: out.NextStepState,
		Home:      home,
	})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if out.Action != "handoff" {
		t.Fatalf("expected handoff action, got %q", out.Action)
	}
	if out.Payload != "default" {
		t.Fatalf("expected payload default, got %q", out.Payload)
	}

	// Verify sentinel was written.
	first, err := firstrun.IsFirstRun(home)
	if err != nil {
		t.Fatalf("IsFirstRun error: %v", err)
	}
	if first {
		t.Error("expected IsFirstRun=false after full walkthrough, got true")
	}
}

func TestOnboardingFlow_unknownStep(t *testing.T) {
	flow := onboardingFlow{}
	st := stepState{Step: "bogus"}
	b, _ := json.Marshal(st)

	out, err := flow.Route(context.Background(), RouteInput{
		StepID:    "bogus",
		StepState: b,
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "error" {
		t.Errorf("Action = %q, want %q", out.Action, "error")
	}
	if !strings.Contains(out.Error, "unknown step") {
		t.Errorf("Error = %q, want 'unknown step'", out.Error)
	}
}

func TestOnboardingFlow_corruptState(t *testing.T) {
	flow := onboardingFlow{}
	out, err := flow.Route(context.Background(), RouteInput{
		StepState: []byte("not-json"),
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "error" {
		t.Errorf("Action = %q, want %q", out.Action, "error")
	}
	if !strings.Contains(out.Error, "corrupt step state") {
		t.Errorf("Error = %q, want 'corrupt step state'", out.Error)
	}
}
