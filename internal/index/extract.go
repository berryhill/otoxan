// Package index provides per-source-type extractors that turn MongoDB documents
// into deterministic text strings suitable for embedding.
package index

import (
	"fmt"
	"strings"

	"github.com/silas/otoxan/internal/store/directives"
	"github.com/silas/otoxan/internal/store/flows"
	"github.com/silas/otoxan/internal/store/notifications"
	"github.com/silas/otoxan/internal/store/plans"
	"github.com/silas/otoxan/internal/store/queue"
	"github.com/silas/otoxan/internal/store/reports"
	"github.com/silas/otoxan/internal/store/tasks"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Extractor extracts deterministic text from a document for embedding.
type Extractor interface {
	Extract(doc bson.M) string
}

// ------------------------------------------------------------------
// Plan
// ------------------------------------------------------------------

type planExtractor struct{}

func (e planExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if title, ok := stringField(doc, "title"); ok && title != "" {
		b.WriteString("Plan: ")
		b.WriteString(title)
		b.WriteString("\n")
	}
	if status, ok := stringField(doc, "status"); ok && status != "" {
		b.WriteString("Status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if content, ok := stringField(doc, "content"); ok && content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	if tags, ok := stringSliceField(doc, "tags"); ok && len(tags) > 0 {
		b.WriteString("Tags: ")
		b.WriteString(strings.Join(tags, ", "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// PlanExtractor returns the extractor for plan documents.
func PlanExtractor() Extractor { return planExtractor{} }

// ------------------------------------------------------------------
// Task
// ------------------------------------------------------------------

type taskExtractor struct{}

func (e taskExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if title, ok := stringField(doc, "title"); ok && title != "" {
		b.WriteString("Task: ")
		b.WriteString(title)
		b.WriteString("\n")
	}
	if status, ok := stringField(doc, "status"); ok && status != "" {
		b.WriteString("Status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if desc, ok := stringField(doc, "description"); ok && desc != "" {
		b.WriteString("Description: ")
		b.WriteString(desc)
		b.WriteString("\n")
	}
	if directive, ok := stringField(doc, "directive"); ok && directive != "" {
		b.WriteString("Directive: ")
		b.WriteString(directive)
		b.WriteString("\n")
	}
	if intent, ok := stringField(doc, "intent"); ok && intent != "" {
		b.WriteString("Intent: ")
		b.WriteString(intent)
		b.WriteString("\n")
	}
	if impl, ok := stringField(doc, "implementation"); ok && impl != "" {
		b.WriteString("Implementation: ")
		b.WriteString(impl)
		b.WriteString("\n")
	}
	if output, ok := stringField(doc, "output"); ok && output != "" {
		b.WriteString("Output: ")
		b.WriteString(output)
		b.WriteString("\n")
	}
	if assignee, ok := stringField(doc, "assignee"); ok && assignee != "" {
		b.WriteString("Assignee: ")
		b.WriteString(assignee)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// TaskExtractor returns the extractor for task documents.
func TaskExtractor() Extractor { return taskExtractor{} }

// ------------------------------------------------------------------
// Report
// ------------------------------------------------------------------

type reportExtractor struct{}

func (e reportExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if title, ok := stringField(doc, "title"); ok && title != "" {
		b.WriteString("Report: ")
		b.WriteString(title)
		b.WriteString("\n")
	}
	if status, ok := stringField(doc, "status"); ok && status != "" {
		b.WriteString("Status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if content, ok := stringField(doc, "content"); ok && content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	if tags, ok := stringSliceField(doc, "tags"); ok && len(tags) > 0 {
		b.WriteString("Tags: ")
		b.WriteString(strings.Join(tags, ", "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// ReportExtractor returns the extractor for report documents.
func ReportExtractor() Extractor { return reportExtractor{} }

// ------------------------------------------------------------------
// Directive
// ------------------------------------------------------------------

type directiveExtractor struct{}

func (e directiveExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if title, ok := stringField(doc, "title"); ok && title != "" {
		b.WriteString("Directive: ")
		b.WriteString(title)
		b.WriteString("\n")
	}
	if category, ok := stringField(doc, "category"); ok && category != "" {
		b.WriteString("Category: ")
		b.WriteString(category)
		b.WriteString("\n")
	}
	if content, ok := stringField(doc, "content"); ok && content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	if enabled, ok := boolField(doc, "enabled"); ok {
		b.WriteString("Enabled: ")
		b.WriteString(fmt.Sprintf("%v", enabled))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// DirectiveExtractor returns the extractor for directive documents.
func DirectiveExtractor() Extractor { return directiveExtractor{} }

// ------------------------------------------------------------------
// TaskEvent
// ------------------------------------------------------------------

type taskEventExtractor struct{}

func (e taskEventExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if eventType, ok := stringField(doc, "event_type"); ok && eventType != "" {
		b.WriteString("Event: ")
		b.WriteString(eventType)
		b.WriteString("\n")
	}
	if taskID, ok := stringField(doc, "task_id"); ok && taskID != "" {
		b.WriteString("Task: ")
		b.WriteString(taskID)
		b.WriteString("\n")
	}
	if data, ok := doc["data"]; ok {
		b.WriteString("Data: ")
		b.WriteString(fmt.Sprintf("%v", data))
		b.WriteString("\n")
	}
	if actor, ok := stringField(doc, "actor"); ok && actor != "" {
		b.WriteString("Actor: ")
		b.WriteString(actor)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// TaskEventExtractor returns the extractor for task event documents.
func TaskEventExtractor() Extractor { return taskEventExtractor{} }

// ------------------------------------------------------------------
// Notification
// ------------------------------------------------------------------

type notificationExtractor struct{}

func (e notificationExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if title, ok := stringField(doc, "title"); ok && title != "" {
		b.WriteString("Notification: ")
		b.WriteString(title)
		b.WriteString("\n")
	}
	if body, ok := stringField(doc, "body"); ok && body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	if channel, ok := stringField(doc, "channel"); ok && channel != "" {
		b.WriteString("Channel: ")
		b.WriteString(channel)
		b.WriteString("\n")
	}
	if status, ok := stringField(doc, "status"); ok && status != "" {
		b.WriteString("Status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// NotificationExtractor returns the extractor for notification documents.
func NotificationExtractor() Extractor { return notificationExtractor{} }

// ------------------------------------------------------------------
// Flow (session_flow / flow_session)
// ------------------------------------------------------------------

type flowExtractor struct{}

func (e flowExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if name, ok := stringField(doc, "name"); ok && name != "" {
		b.WriteString("Flow: ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	if desc, ok := stringField(doc, "description"); ok && desc != "" {
		b.WriteString(desc)
		b.WriteString("\n")
	}
	if status, ok := stringField(doc, "status"); ok && status != "" {
		b.WriteString("Status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if notes, ok := stringField(doc, "notes"); ok && notes != "" {
		b.WriteString("Notes: ")
		b.WriteString(notes)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// FlowExtractor returns the extractor for flow documents.
func FlowExtractor() Extractor { return flowExtractor{} }

// ------------------------------------------------------------------
// Session
// ------------------------------------------------------------------

type sessionExtractor struct{}

func (e sessionExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if user, ok := stringField(doc, "user_content"); ok && user != "" {
		b.WriteString("User: ")
		b.WriteString(user)
		b.WriteString("\n")
	}
	if assistant, ok := stringField(doc, "assistant_content"); ok && assistant != "" {
		b.WriteString("Assistant: ")
		b.WriteString(assistant)
		b.WriteString("\n")
	}
	if content, ok := stringField(doc, "content"); ok && content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// SessionExtractor returns the extractor for session documents.
func SessionExtractor() Extractor { return sessionExtractor{} }

// ------------------------------------------------------------------
// Build
// ------------------------------------------------------------------

type buildExtractor struct{}

func (e buildExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if output, ok := stringField(doc, "output"); ok && output != "" {
		b.WriteString("Build Output:\n")
		b.WriteString(output)
		b.WriteString("\n")
	}
	if errTrace, ok := stringField(doc, "error_trace"); ok && errTrace != "" {
		b.WriteString("Error Trace:\n")
		b.WriteString(errTrace)
		b.WriteString("\n")
	}
	if status, ok := stringField(doc, "status"); ok && status != "" {
		b.WriteString("Status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// BuildExtractor returns the extractor for build documents.
func BuildExtractor() Extractor { return buildExtractor{} }

// ------------------------------------------------------------------
// Error
// ------------------------------------------------------------------

type errorExtractor struct{}

func (e errorExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if msg, ok := stringField(doc, "message"); ok && msg != "" {
		b.WriteString("Error: ")
		b.WriteString(msg)
		b.WriteString("\n")
	}
	if stack, ok := stringField(doc, "stack_trace"); ok && stack != "" {
		b.WriteString("Stack Trace:\n")
		b.WriteString(stack)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// ErrorExtractor returns the extractor for error documents.
func ErrorExtractor() Extractor { return errorExtractor{} }

// ------------------------------------------------------------------
// Run
// ------------------------------------------------------------------

type runExtractor struct{}

func (e runExtractor) Extract(doc bson.M) string {
	var b strings.Builder
	if status, ok := stringField(doc, "status"); ok && status != "" {
		b.WriteString("Run Status: ")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if errMsg, ok := stringField(doc, "error_message"); ok && errMsg != "" {
		b.WriteString("Error: ")
		b.WriteString(errMsg)
		b.WriteString("\n")
	}
	if output, ok := stringField(doc, "output"); ok && output != "" {
		b.WriteString("Output: ")
		b.WriteString(output)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// RunExtractor returns the extractor for run documents.
func RunExtractor() Extractor { return runExtractor{} }

// ------------------------------------------------------------------
// Typed helpers (convenience wrappers that decode bson.M first)
// ------------------------------------------------------------------

// ExtractPlan extracts text from a plans.Plan.
func ExtractPlan(p *plans.Plan) string {
	doc, _ := toBSONM(p)
	return PlanExtractor().Extract(doc)
}

// ExtractTask extracts text from a tasks.Task.
func ExtractTask(t *tasks.Task) string {
	doc, _ := toBSONM(t)
	return TaskExtractor().Extract(doc)
}

// ExtractReport extracts text from a reports.Report.
func ExtractReport(r *reports.Report) string {
	doc, _ := toBSONM(r)
	return ReportExtractor().Extract(doc)
}

// ExtractDirective extracts text from a directives.Directive.
func ExtractDirective(d *directives.Directive) string {
	doc, _ := toBSONM(d)
	return DirectiveExtractor().Extract(doc)
}

// ExtractTaskEvent extracts text from a queue.TaskEvent.
func ExtractTaskEvent(e *queue.TaskEvent) string {
	doc, _ := toBSONM(e)
	return TaskEventExtractor().Extract(doc)
}

// ExtractNotification extracts text from a notifications.Notification.
func ExtractNotification(n *notifications.Notification) string {
	doc, _ := toBSONM(n)
	return NotificationExtractor().Extract(doc)
}

// ExtractFlow extracts text from a flows.Flow.
func ExtractFlow(f *flows.Flow) string {
	doc, _ := toBSONM(f)
	return FlowExtractor().Extract(doc)
}

// ------------------------------------------------------------------
// bson.M helpers
// ------------------------------------------------------------------

func stringField(doc bson.M, key string) (string, bool) {
	v, ok := doc[key]
	if !ok {
		return "", false
	}
	switch val := v.(type) {
	case string:
		return val, true
	case *string:
		if val == nil {
			return "", false
		}
		return *val, true
	default:
		return "", false
	}
}

func stringSliceField(doc bson.M, key string) ([]string, bool) {
	v, ok := doc[key]
	if !ok {
		return nil, false
	}
	switch val := v.(type) {
	case []string:
		return val, true
	case []interface{}:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func boolField(doc bson.M, key string) (bool, bool) {
	v, ok := doc[key]
	if !ok {
		return false, false
	}
	switch val := v.(type) {
	case bool:
		return val, true
	default:
		return false, false
	}
}

func toBSONM(v interface{}) (bson.M, error) {
	b, err := bson.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m bson.M
	if err := bson.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
