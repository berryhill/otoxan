package plans

import (
	"testing"
)

func TestExtractTasks_Basic(t *testing.T) {
	content := `### T1: First task
**Status:** PENDING
**Depends on:** (none)
**Assigned:** silas
**Tool:** bash
**Verify:** output matches
**Parent Provider:** openai

### T2: Second task
**Status:** RUNNING
**Depends on:** T1
**Assigned:** archer
**Tool:** go
**Verify:** tests pass
`

	tasks := ExtractTasks(content)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	if tasks[0].ID != "T1" {
		t.Fatalf("expected T1, got %s", tasks[0].ID)
	}
	if tasks[0].Title != "First task" {
		t.Fatalf("expected 'First task', got %s", tasks[0].Title)
	}
	if tasks[0].Status != "PENDING" {
		t.Fatalf("expected PENDING, got %s", tasks[0].Status)
	}
	if tasks[0].Assigned != "silas" {
		t.Fatalf("expected silas, got %s", tasks[0].Assigned)
	}
	if tasks[0].Tool != "bash" {
		t.Fatalf("expected bash, got %s", tasks[0].Tool)
	}
	if tasks[0].Verify != "output matches" {
		t.Fatalf("expected 'output matches', got %s", tasks[0].Verify)
	}
	if tasks[0].ParentProvider != "openai" {
		t.Fatalf("expected openai, got %s", tasks[0].ParentProvider)
	}
	if len(tasks[0].DependsOn) != 0 {
		t.Fatalf("expected no deps, got %v", tasks[0].DependsOn)
	}

	if tasks[1].ID != "T2" {
		t.Fatalf("expected T2, got %s", tasks[1].ID)
	}
	if tasks[1].Title != "Second task" {
		t.Fatalf("expected 'Second task', got %s", tasks[1].Title)
	}
	if tasks[1].Status != "RUNNING" {
		t.Fatalf("expected RUNNING, got %s", tasks[1].Status)
	}
	if len(tasks[1].DependsOn) != 1 || tasks[1].DependsOn[0] != "T1" {
		t.Fatalf("expected [T1], got %v", tasks[1].DependsOn)
	}
}

func TestExtractTasks_MultiDepends(t *testing.T) {
	content := `### T3: Complex task
**Status:** SUCCESS
**Depends on:** T1, T2, T4
**Assigned:** luca
`

	tasks := ExtractTasks(content)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if len(tasks[0].DependsOn) != 3 {
		t.Fatalf("expected 3 deps, got %v", tasks[0].DependsOn)
	}
	if tasks[0].DependsOn[0] != "T1" || tasks[0].DependsOn[1] != "T2" || tasks[0].DependsOn[2] != "T4" {
		t.Fatalf("expected [T1 T2 T4], got %v", tasks[0].DependsOn)
	}
}

func TestExtractTasks_NoTasks(t *testing.T) {
	content := `# Just a heading
Some body text without task headings.
`

	tasks := ExtractTasks(content)
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestExtractTasks_Defaults(t *testing.T) {
	content := `### T99: Minimal task
Some body without any metadata lines.
`

	tasks := ExtractTasks(content)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != "PENDING" {
		t.Fatalf("expected default PENDING, got %s", tasks[0].Status)
	}
	if tasks[0].Assigned != "" {
		t.Fatalf("expected empty assigned, got %s", tasks[0].Assigned)
	}
	if tasks[0].Tool != "" {
		t.Fatalf("expected empty tool, got %s", tasks[0].Tool)
	}
	if tasks[0].Verify != "" {
		t.Fatalf("expected empty verify, got %s", tasks[0].Verify)
	}
	if tasks[0].ParentProvider != "" {
		t.Fatalf("expected empty parent_provider, got %s", tasks[0].ParentProvider)
	}
}
