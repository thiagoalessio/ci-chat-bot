package workflowSubmissionEvents

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack/slackevents"
)

func TestSlackWorkflowClientSerialization(t *testing.T) {
	t.Run("stepCompleted sends correct JSON", func(t *testing.T) {
		var gotBody map[string]interface{}
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
		outputsMap := gotBody["outputs"].(map[string]interface{})
		if outputsMap["issue.key"] != "PROJ-123" {
			t.Errorf("outputs[issue.key] = %v, want PROJ-123", outputsMap["issue.key"])
		}
	})

	t.Run("stepFailed sends correct JSON", func(t *testing.T) {
		var gotBody map[string]interface{}
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
		errorObj := gotBody["error"].(map[string]interface{})
		if errorObj["message"] != "something broke" {
			t.Errorf("error.message = %v, want 'something broke'", errorObj["message"])
		}
	})

	t.Run("saveWorkflowStepConfiguration sends correct JSON", func(t *testing.T) {
		var gotBody map[string]interface{}
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
		inputsMap := gotBody["inputs"].(map[string]interface{})
		ticketType := inputsMap["ticket_type"].(map[string]interface{})
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
	var innerData interface{}
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
	// Simulate the exact JSON that Slack sends for a workflow_step_execute event,
	// parse it through slackevents.ParseEvent (which requires the init()
	// registration), then through the handler's marshal/unmarshal round-trip.

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
					"user_details": {"value": "U12345"}
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

	// Parse the same way the bot's handleEvent does (slackevents.ParseEvent)
	event, err := slackevents.ParseEvent(rawPayload, slackevents.OptionNoVerifyToken())
	if err != nil {
		t.Fatalf("ParseEvent failed (init() registration may be missing): %v", err)
	}

	if event.Type != slackevents.CallbackEvent {
		t.Fatalf("event.Type = %q, want %q", event.Type, slackevents.CallbackEvent)
	}

	// Now do the same marshal/unmarshal the handler does
	raw, err := json.Marshal(event.InnerEvent.Data)
	if err != nil {
		t.Fatalf("failed to marshal InnerEvent.Data: %v", err)
	}

	var wsEvent workflowStepExecuteEvent
	if err := json.Unmarshal(raw, &wsEvent); err != nil {
		t.Fatalf("failed to unmarshal workflowStepExecuteEvent: %v", err)
	}

	if wsEvent.Type != "workflow_step_execute" {
		t.Errorf("event type = %q, want workflow_step_execute", wsEvent.Type)
	}
	if wsEvent.CallbackID != "jira_ticket" {
		t.Errorf("callback_id = %q, want jira_ticket", wsEvent.CallbackID)
	}
	if wsEvent.WorkflowStep.WorkflowStepExecuteID != "exec-test-123" {
		t.Errorf("execute_id = %q, want exec-test-123", wsEvent.WorkflowStep.WorkflowStepExecuteID)
	}
	if wsEvent.WorkflowStep.Inputs["ticket_type"].Value != "bug" {
		t.Errorf("ticket_type = %q, want bug", wsEvent.WorkflowStep.Inputs["ticket_type"].Value)
	}
	if wsEvent.WorkflowStep.Inputs["ticket_title"].Value != "Test Bug" {
		t.Errorf("ticket_title = %q, want 'Test Bug'", wsEvent.WorkflowStep.Inputs["ticket_title"].Value)
	}
	if len(wsEvent.WorkflowStep.Outputs) != 2 {
		t.Errorf("outputs count = %d, want 2", len(wsEvent.WorkflowStep.Outputs))
	}

}
