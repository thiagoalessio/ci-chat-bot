package workflowSubmissionEvents

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	jiraAPI "github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack/slackevents"
)

type mockIssueFiler struct {
	issue *jiraAPI.Issue
	err   error
	calls []mockFileIssueCall
}

type mockFileIssueCall struct {
	IssueType   string
	Title       string
	Description string
	Reporter    string
}

func (m *mockIssueFiler) FileIssue(issueType, title, description, reporter string, _ *logrus.Entry) (*jiraAPI.Issue, error) {
	m.calls = append(m.calls, mockFileIssueCall{
		IssueType:   issueType,
		Title:       title,
		Description: description,
		Reporter:    reporter,
	})
	return m.issue, m.err
}

type mockWorkflowClient struct {
	completedCalls []mockCompletedCall
	failedCalls    []mockFailedCall
	completeErr    error
	failErr        error
}

type mockCompletedCall struct {
	ExecuteID string
	Outputs   map[string]string
}

type mockFailedCall struct {
	ExecuteID string
	Message   string
}

func (m *mockWorkflowClient) WorkflowStepCompleted(execID string, outputs map[string]string) error {
	m.completedCalls = append(m.completedCalls, mockCompletedCall{ExecuteID: execID, Outputs: outputs})
	return m.completeErr
}

func (m *mockWorkflowClient) WorkflowStepFailed(execID string, msg string) error {
	m.failedCalls = append(m.failedCalls, mockFailedCall{ExecuteID: execID, Message: msg})
	return m.failErr
}

func TestSlackWorkflowClientSerialization(t *testing.T) {
	t.Run("stepCompleted sends correct JSON", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Errorf("failed to unmarshal request body: %v", err)
			}

			if r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
			}
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("Authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		}))
		defer srv.Close()

		client := &SlackWorkflowClient{token: "test-token", endpoint: srv.URL + "/"}
		outputs := map[string]string{"issue.key": "PROJ-123", "issue.link": "https://issues.redhat.com/browse/PROJ-123"}
		err := client.WorkflowStepCompleted("exec-id-1", outputs)
		if err != nil {
			t.Fatalf("WorkflowStepCompleted returned error: %v", err)
		}

		if gotBody["workflow_step_execute_id"] != "exec-id-1" {
			t.Errorf("workflow_step_execute_id = %v, want exec-id-1", gotBody["workflow_step_execute_id"])
		}
		outputsMap := gotBody["outputs"].(map[string]any)
		if outputsMap["issue.key"] != "PROJ-123" {
			t.Errorf("outputs[issue.key] = %v, want PROJ-123", outputsMap["issue.key"])
		}
	})

	t.Run("stepFailed sends correct JSON", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Errorf("failed to unmarshal request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		}))
		defer srv.Close()

		client := &SlackWorkflowClient{token: "test-token", endpoint: srv.URL + "/"}
		err := client.WorkflowStepFailed("exec-id-2", "something broke")
		if err != nil {
			t.Fatalf("WorkflowStepFailed returned error: %v", err)
		}

		if gotBody["workflow_step_execute_id"] != "exec-id-2" {
			t.Errorf("workflow_step_execute_id = %v, want exec-id-2", gotBody["workflow_step_execute_id"])
		}
		errorObj := gotBody["error"].(map[string]any)
		if errorObj["message"] != "something broke" {
			t.Errorf("error.message = %v, want 'something broke'", errorObj["message"])
		}
	})

	t.Run("saveWorkflowStepConfiguration sends correct JSON", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Errorf("failed to unmarshal request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		}))
		defer srv.Close()

		client := &SlackWorkflowClient{token: "test-token", endpoint: srv.URL + "/"}
		inputs := WorkflowStepInputs{
			"ticket_type":  {Value: "bug", SkipVariableReplacement: false},
			"ticket_title": {Value: "Test bug"},
		}
		outputs := []WorkflowStepOutput{
			{Name: "issue.key", Type: "text", Label: "Issue Key"},
		}
		err := client.SaveWorkflowStepConfiguration("edit-id-1", inputs, outputs)
		if err != nil {
			t.Fatalf("SaveWorkflowStepConfiguration returned error: %v", err)
		}

		if gotBody["workflow_step_edit_id"] != "edit-id-1" {
			t.Errorf("workflow_step_edit_id = %v, want edit-id-1", gotBody["workflow_step_edit_id"])
		}
		inputsMap := gotBody["inputs"].(map[string]any)
		ticketType := inputsMap["ticket_type"].(map[string]any)
		if ticketType["value"] != "bug" {
			t.Errorf("inputs.ticket_type.value = %v, want bug", ticketType["value"])
		}
	})

	t.Run("API error is returned", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":false,"error":"invalid_auth"}`)
		}))
		defer srv.Close()

		client := &SlackWorkflowClient{token: "bad-token", endpoint: srv.URL + "/"}
		err := client.WorkflowStepCompleted("exec-id", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "slack API workflows.stepCompleted: invalid_auth" {
			t.Errorf("error = %q, want slack API error", err.Error())
		}
	})
}

func TestWorkflowStepExecuteEventParsing(t *testing.T) {
	rawEvent := `{
		"type": "workflow_step_execute",
		"callback_id": "jira_ticket",
		"workflow_step": {
			"workflow_step_execute_id": "exec-123",
			"workflow_id": "wf-456",
			"workflow_instance_id": "inst-789",
			"step_id": "step-1",
			"inputs": {
				"ticket_type": {"value": "bug", "skip_variable_replacement": false},
				"ticket_title": {"value": "Test Bug Title"}
			},
			"outputs": [
				{"name": "issue.key", "type": "text", "label": "Issue Key"},
				{"name": "issue.link", "type": "text", "label": "Issue Link"}
			]
		},
		"event_ts": "1234567890.123456"
	}`

	// Simulate the marshal/unmarshal round-trip the handler does
	var innerData any
	if err := json.Unmarshal([]byte(rawEvent), &innerData); err != nil {
		t.Fatalf("failed to unmarshal raw event: %v", err)
	}

	raw, err := json.Marshal(innerData)
	if err != nil {
		t.Fatalf("failed to re-marshal: %v", err)
	}

	var event workflowStepExecuteEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("failed to unmarshal into workflowStepExecuteEvent: %v", err)
	}

	if event.Type != "workflow_step_execute" {
		t.Errorf("Type = %q, want workflow_step_execute", event.Type)
	}
	if event.CallbackID != "jira_ticket" {
		t.Errorf("CallbackID = %q, want jira_ticket", event.CallbackID)
	}
	if event.WorkflowStep.WorkflowStepExecuteID != "exec-123" {
		t.Errorf("WorkflowStepExecuteID = %q, want exec-123", event.WorkflowStep.WorkflowStepExecuteID)
	}
	if len(event.WorkflowStep.Inputs) != 2 {
		t.Errorf("Inputs count = %d, want 2", len(event.WorkflowStep.Inputs))
	}
	if event.WorkflowStep.Inputs["ticket_type"].Value != "bug" {
		t.Errorf("ticket_type = %q, want bug", event.WorkflowStep.Inputs["ticket_type"].Value)
	}
	if len(event.WorkflowStep.Outputs) != 2 {
		t.Errorf("Outputs count = %d, want 2", len(event.WorkflowStep.Outputs))
	}
	if event.WorkflowStep.Outputs[0].Name != "issue.key" {
		t.Errorf("first output name = %q, want issue.key", event.WorkflowStep.Outputs[0].Name)
	}
}

func TestHandlerRoutesWorkflowStepExecute(t *testing.T) {
	handler := Handler("fake-token", nil)

	t.Run("ignores non-callback events", func(t *testing.T) {
		event := &slackevents.EventsAPIEvent{
			Type: slackevents.URLVerification,
		}
		handled, err := handler.Handle(event, nil)
		if handled || err != nil {
			t.Errorf("expected (false, nil), got (%v, %v)", handled, err)
		}
	})

	t.Run("ignores non-workflow_step_execute inner events", func(t *testing.T) {
		event := &slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{},
			},
		}
		handled, err := handler.Handle(event, nil)
		if handled {
			t.Error("expected handled=false for MessageEvent")
		}
		_ = err
	})
}

func TestHandlerEndToEndJiraTicket(t *testing.T) {
	rawPayload := []byte(`{
		"token": "fake",
		"team_id": "T0001",
		"api_app_id": "A0001",
		"event": {
			"type": "workflow_step_execute",
			"callback_id": "jira_ticket",
			"workflow_step": {
				"workflow_step_execute_id": "exec-test-123",
				"workflow_id": "wf-001",
				"workflow_instance_id": "inst-001",
				"step_id": "step-001",
				"inputs": {
					"ticket_type": {"value": "bug", "skip_variable_replacement": false},
					"ticket_title": {"value": "Test Bug"},
					"user_details": {"value": "U12345"},
					"incorrect_behaviour": {"value": "it breaks"},
					"expected_behaviour": {"value": "it works"},
					"impact": {"value": "high"},
					"affected_component": {"value": "Testing"},
					"is_reproducible": {"value": "yes"}
				},
				"outputs": [
					{"name": "issue.key", "type": "text", "label": "Issue Key"},
					{"name": "issue.link", "type": "text", "label": "Issue Link"}
				]
			},
			"event_ts": "1234567890.123456"
		},
		"type": "event_callback",
		"event_id": "Ev0001",
		"event_time": 1234567890
	}`)

	t.Run("successful jira filing calls WorkflowStepCompleted", func(t *testing.T) {
		event, err := slackevents.ParseEvent(rawPayload, slackevents.OptionNoVerifyToken())
		if err != nil {
			t.Fatalf("ParseEvent failed (init() registration may be missing): %v", err)
		}

		filer := &mockIssueFiler{issue: &jiraAPI.Issue{Key: "OCPCRT-999"}}
		wc := &mockWorkflowClient{}
		handler := newHandler(wc, filer)
		logger := logrus.NewEntry(logrus.New())

		handled, err := handler.Handle(&event, logger)
		if err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}
		if !handled {
			t.Fatal("expected handled=true for jira_ticket callback")
		}

		if len(filer.calls) != 1 {
			t.Fatalf("FileIssue called %d times, want 1", len(filer.calls))
		}
		if filer.calls[0].IssueType != "Bug" {
			t.Errorf("IssueType = %q, want Bug", filer.calls[0].IssueType)
		}
		if filer.calls[0].Title != "Test Bug" {
			t.Errorf("Title = %q, want 'Test Bug'", filer.calls[0].Title)
		}
		if filer.calls[0].Reporter != "U12345" {
			t.Errorf("Reporter = %q, want U12345", filer.calls[0].Reporter)
		}

		if len(wc.completedCalls) != 1 {
			t.Fatalf("WorkflowStepCompleted called %d times, want 1", len(wc.completedCalls))
		}
		if wc.completedCalls[0].ExecuteID != "exec-test-123" {
			t.Errorf("ExecuteID = %q, want exec-test-123", wc.completedCalls[0].ExecuteID)
		}
		if wc.completedCalls[0].Outputs["issue.key"] != "OCPCRT-999" {
			t.Errorf("issue.key = %q, want OCPCRT-999", wc.completedCalls[0].Outputs["issue.key"])
		}
		if wc.completedCalls[0].Outputs["issue.link"] != "https://issues.redhat.com/browse/OCPCRT-999" {
			t.Errorf("issue.link = %q, want OCPCRT-999 browse URL", wc.completedCalls[0].Outputs["issue.link"])
		}
		if len(wc.failedCalls) != 0 {
			t.Errorf("WorkflowStepFailed called %d times, want 0", len(wc.failedCalls))
		}
	})

	t.Run("jira filing error calls WorkflowStepFailed", func(t *testing.T) {
		event, err := slackevents.ParseEvent(rawPayload, slackevents.OptionNoVerifyToken())
		if err != nil {
			t.Fatalf("ParseEvent failed: %v", err)
		}

		filer := &mockIssueFiler{err: fmt.Errorf("jira unavailable")}
		wc := &mockWorkflowClient{}
		handler := newHandler(wc, filer)
		logger := logrus.NewEntry(logrus.New())

		handled, err := handler.Handle(&event, logger)
		if handled {
			t.Error("expected handled=false when jira filing fails")
		}
		if err == nil || err.Error() != "jira unavailable" {
			t.Errorf("err = %v, want 'jira unavailable'", err)
		}

		if len(wc.failedCalls) != 1 {
			t.Fatalf("WorkflowStepFailed called %d times, want 1", len(wc.failedCalls))
		}
		if wc.failedCalls[0].ExecuteID != "exec-test-123" {
			t.Errorf("ExecuteID = %q, want exec-test-123", wc.failedCalls[0].ExecuteID)
		}
		if wc.failedCalls[0].Message != "jira unavailable" {
			t.Errorf("Message = %q, want 'jira unavailable'", wc.failedCalls[0].Message)
		}
		if len(wc.completedCalls) != 0 {
			t.Errorf("WorkflowStepCompleted called %d times, want 0", len(wc.completedCalls))
		}
	})

	t.Run("nil filer calls WorkflowStepFailed", func(t *testing.T) {
		event, err := slackevents.ParseEvent(rawPayload, slackevents.OptionNoVerifyToken())
		if err != nil {
			t.Fatalf("ParseEvent failed: %v", err)
		}

		wc := &mockWorkflowClient{}
		handler := newHandler(wc, nil)
		logger := logrus.NewEntry(logrus.New())

		handled, err := handler.Handle(&event, logger)
		if handled {
			t.Error("expected handled=false when filer is nil")
		}
		if err == nil || err.Error() != "jira client is not configured" {
			t.Errorf("err = %v, want 'jira client is not configured'", err)
		}

		if len(wc.failedCalls) != 1 {
			t.Fatalf("WorkflowStepFailed called %d times, want 1", len(wc.failedCalls))
		}
		if wc.failedCalls[0].Message != "jira client is not configured" {
			t.Errorf("Message = %q, want 'jira client is not configured'", wc.failedCalls[0].Message)
		}
	})
}
