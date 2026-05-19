package clickup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/aspectrr/ivy/internal/vine/orchestrator"
)

// TestE2E_MentionAndThreadFollowUp simulates the full flow:
// 1. User mentions the agent in a ClickUp task
// 2. Agent calls tools (list_parser_hosts, exec_on_host) and posts a response
// 3. User follows up in the thread mentioning the agent
// 4. Agent creates a sandbox, tests changes, and replies in the thread
func TestE2E_MentionAndThreadFollowUp(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Mock ClickUp Server ────────────────────────────────────
	ms := NewMockServer(logger)
	defer ms.Close()

	cfg := config.ClickUpConfig{
		TeamID:        "team1",
		AgentUsername: "Ivy Agent",
		PollInterval:  50 * time.Millisecond,
	}
	clickupClient := NewMockClient(ms, cfg, logger)

	// Seed a task
	taskID := "task-investigate-logs"
	ms.AddTask(Task{
		ID:          taskID,
		Name:        "Investigate Log Pipeline Issues",
		Status:      Status{Status: "open"},
		Description: "We're seeing grok parse failures in the production log pipeline.",
		URL:         "https://app.clickup.com/t/" + taskID,
	})

	// ── In-memory stores ───────────────────────────────────────
	sessions := newMemSessionStore()
	events := newMemEventStore()

	// ── Fake LLM (scripted responses) ──────────────────────────
	// Phase 1: Initial mention — agent investigates
	//   Turn 1: Call list_parser_hosts
	//   Turn 2: Call exec_on_host on parser-01
	//   Turn 3: Post final comment
	llm := &fakeLLMClient{
		responses: []scriptedResponse{
			// Turn 1: list parser hosts
			{
				ToolCalls: []orchestrator.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: orchestrator.FunctionCall{
							Name:      "list_parser_hosts",
							Arguments: `{}`,
						},
					},
				},
				FinishReason: "tool_calls",
			},
			// Turn 2: exec on host to check logs
			{
				ToolCalls: []orchestrator.ToolCall{
					{
						ID:   "call_2",
						Type: "function",
						Function: orchestrator.FunctionCall{
							Name:      "exec_on_host",
							Arguments: `{"host_id":"parser-01","command":"journalctl -u logstash --since '10 min ago' | tail -20"}`,
						},
					},
				},
				FinishReason: "tool_calls",
			},
			// Turn 3: post final comment with findings
			{
				Content:      "I've investigated the log parser hosts and found grok parse failures on parser-01. The HTTP access log pattern is missing from the grok config. I'll post my findings.",
				FinishReason: "tool_calls",
				ToolCalls: []orchestrator.ToolCall{
					{
						ID:   "call_3",
						Type: "function",
						Function: orchestrator.FunctionCall{
							Name:      "clickup_post_comment",
							Arguments: `{"task_id":"task-investigate-logs","text":"## Investigation Results\n\nI checked both log parser hosts. Here's what I found:\n\n**parser-01** (log-parser-01.prod):\n- Processing ~2.3k events/s\n- Seeing grok parse failures for HTTP access log messages\n- Pattern: ` + "`GET /api/health 200 3ms`" + `\n- The grok pattern for HTTP access logs appears to be missing\n\n**parser-02** (log-parser-02.prod):\n- Not checked yet, but likely has the same issue\n\nRecommendation: Add the missing grok pattern to the filter config."}`,
						},
					},
				},
			},
			// Turn 4: just the stop after posting comment
			{
				Content:      "Done investigating.",
				FinishReason: "stop",
			},

			// Phase 2: Thread follow-up — agent creates sandbox and tests
			// Turn 5: create_sandbox
			{
				ToolCalls: []orchestrator.ToolCall{
					{
						ID:   "call_5",
						Type: "function",
						Function: orchestrator.FunctionCall{
							Name:      "create_sandbox",
							Arguments: `{"host_id":"parser-01"}`,
						},
					},
				},
				FinishReason: "tool_calls",
			},
			// Turn 6: check pipeline health
			{
				ToolCalls: []orchestrator.ToolCall{
					{
						ID:   "call_6",
						Type: "function",
						Function: orchestrator.FunctionCall{
							Name:      "pipeline_health",
							Arguments: `{}`,
						},
					},
				},
				FinishReason: "tool_calls",
			},
			// Turn 7: reply in thread with results
			{
				Content:      "I've created a sandbox and tested the fix. Posting results in the thread.",
				FinishReason: "tool_calls",
				ToolCalls: []orchestrator.ToolCall{
					{
						ID:   "call_7",
						Type: "function",
						Function: orchestrator.FunctionCall{
							Name:      "clickup_reply_comment",
							Arguments: `{"comment_id":"__PARENT_COMMENT_ID__","text":"## Sandbox Test Results\n\n✅ Created sandbox mirroring parser-01 (Logstash 8.12.0)\n✅ Pipeline is healthy (Redpanda + Logstash + Elasticsearch)\n✅ Processed 1,500 messages with 0 errors\n\nThe fix is ready for deployment."}`,
						},
					},
				},
			},
			// Turn 8: stop
			{
				Content:      "Sandbox tested successfully.",
				FinishReason: "stop",
			},
		},
	}

	// ── Fake tool executor ─────────────────────────────────────
	toolExec := &fakeToolExecutor{clickupClient: clickupClient}

	// ── Test orchestrator ──────────────────────────────────────
	orch := newTestOrchestrator(sessions, events, llm, toolExec, logger)
	orch.SetTools([]orchestrator.ToolDef{
		{Type: "function", Function: orchestrator.FunctionDef{Name: "list_parser_hosts", Description: "List parser hosts"}},
		{Type: "function", Function: orchestrator.FunctionDef{Name: "exec_on_host", Description: "Execute command on host"}},
		{Type: "function", Function: orchestrator.FunctionDef{Name: "clickup_post_comment", Description: "Post ClickUp comment"}},
		{Type: "function", Function: orchestrator.FunctionDef{Name: "clickup_reply_comment", Description: "Reply in ClickUp thread"}},
		{Type: "function", Function: orchestrator.FunctionDef{Name: "create_sandbox", Description: "Create pipeline sandbox"}},
		{Type: "function", Function: orchestrator.FunctionDef{Name: "pipeline_health", Description: "Check pipeline health"}},
	})

	// ── Set up handler (mirrors main.go logic) ─────────────────
	handler := func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {
		sourceID := task.ID
		logger.Info("handler invoked", "task_id", sourceID, "reason", reason, "is_new", isNew)

		if reason == ReasonMentioned && mention != nil {
			// Fetch comments for context
			comments, _ := clickupClient.GetComments(ctx, sourceID)
			var commentsContext string
			if len(comments) > 0 {
				var sb strings.Builder
				sb.WriteString("\n\n--- Comments ---\n")
				for _, c := range comments {
					fmt.Fprintf(&sb, "[%s]: %s\n", c.User.Username, c.CommentText)
				}
				commentsContext = sb.String()
			}

			sess, err := sessions.GetBySource(ctx, "clickup", sourceID)
			if err != nil || sess == nil {
				// No existing session — create new
				sess, err = sessions.Create(ctx, "clickup", sourceID, json.RawMessage(`{}`))
				if err != nil {
					logger.Error("failed to create session", "error", err)
					return
				}

				mentionCommentInfo := fmt.Sprintf("\nMention Comment ID: %s (use clickup_reply_comment with this ID to reply in-thread)", mention.CommentID.String())
				mentionContext := fmt.Sprintf("[ClickUp Task: %s — %s mentioned the agent]\nURL: %s\nStatus: %s\n\n%s%s%s",
					task.Name, mention.Author, task.URL, task.Status.Status, task.Description, commentsContext, mentionCommentInfo)

				if _, err := events.Append(ctx, sess.ID, model.EventTypeUserMessage, mustJSONE2E(model.UserMessagePayload{
					Content: mentionContext,
				})); err != nil {
					logger.Error("failed to seed mention message", "error", err)
					return
				}

				if err := orch.StartRun(ctx, sess.ID); err != nil {
					logger.Error("failed to start run", "error", err)
					return
				}

				logger.Info("started agent run for mention", "session_id", sess.ID)
				return
			}

			// Existing session — handle re-mention
			if sess.Status == model.StatusCompleted || sess.Status == model.StatusRunning {
				if sess.Status == model.StatusRunning {
					_ = orch.Terminate(ctx, sess.ID, true)
				}
				_ = sessions.ClearSource(ctx, sess.ID)

				sess, err = sessions.Create(ctx, "clickup", sourceID, json.RawMessage(`{}`))
				if err != nil {
					return
				}

				mentionContext := fmt.Sprintf("[ClickUp Task: %s — %s mentioned the agent again]\nURL: %s\n\n%s%s",
					task.Name, mention.Author, task.URL, task.Description, commentsContext)

				events.Append(ctx, sess.ID, model.EventTypeUserMessage, mustJSONE2E(model.UserMessagePayload{
					Content: mentionContext,
				}))
				orch.StartRun(ctx, sess.ID)
				return
			}
		}

		if reason == ReasonThreadMentioned && mention != nil {
			logger.Info("thread mention detected", "parent_comment_id", mention.ParentCommentID)

			sess, err := sessions.GetBySource(ctx, "clickup", sourceID)
			if err != nil || sess == nil {
				logger.Warn("no existing session for thread mention")
				return
			}

			if sess.Status == model.StatusRunning {
				_ = orch.Terminate(ctx, sess.ID, true)
			}

			if sess.Status == model.StatusCompleted || sess.Status == model.StatusFailed {
				_ = sessions.ClearSource(ctx, sess.ID)

				newSess, err := sessions.Create(ctx, "clickup", sourceID, json.RawMessage(`{}`))
				if err != nil {
					logger.Error("failed to create new session", "error", err)
					return
				}

				followUpMsg := fmt.Sprintf("[ClickUp Thread Follow-up: %s — %s replied]\nTask: %s\nURL: %s\n\n[%s]: %s\n\nIMPORTANT: You are responding in a thread. Use clickup_reply_comment with comment_id=%s to reply. Do NOT use clickup_post_comment.",
					task.Name, mention.Author, task.Name, task.URL,
					mention.Author, mention.CommentText, mention.ParentCommentID.String())

				events.Append(ctx, newSess.ID, model.EventTypeUserMessage, mustJSONE2E(model.UserMessagePayload{
					Content: followUpMsg,
				}))
				orch.StartRun(ctx, newSess.ID)
				logger.Info("started new session for thread mention", "session_id", newSess.ID)
				return
			}

			// Suspended — resume
			followUpMsg := fmt.Sprintf("[%s replied in thread]\n\"%s\"\n\nIMPORTANT: Use clickup_reply_comment with comment_id=%s.",
				mention.Author, mention.CommentText, mention.ParentCommentID.String())
			events.Append(ctx, sess.ID, model.EventTypeUserMessage, mustJSONE2E(model.UserMessagePayload{
				Content: followUpMsg,
			}))
			orch.Resume(ctx, sess.ID)
		}
	}

	// ── Create poller ──────────────────────────────────────────
	poller := NewPoller(clickupClient, cfg, handler, logger)
	poller.Start(ctx)

	// Wait for poller to initialize its lastUpdated timestamp
	time.Sleep(100 * time.Millisecond)

	// ════════════════════════════════════════════════════════════
	// PHASE 1: User mentions the agent
	// ════════════════════════════════════════════════════════════
	t.Log("=== PHASE 1: User mentions the agent ===")

	// Post the mention — the mock server uses time.Now() for the comment date,
	// which will be after the poller's lastUpdated.
	parentCommentID := ms.PostMention(taskID, "Collin",
		"Hey @Ivy Agent can you investigate what logs are coming from the existing log parser hosts?")

	// Update the task's DateUpdated to trigger the poller
	ms.mu.Lock()
	if task, ok := ms.tasks[taskID]; ok {
		task.DateUpdated = fmt.Sprintf("%d", time.Now().UnixMilli())
		task.DateCreated = fmt.Sprintf("%d", time.Now().Add(-1*time.Hour).UnixMilli())
	}
	ms.mu.Unlock()

	t.Logf("Mention comment posted: %s", parentCommentID)

	// Wait for the first session to complete
	time.Sleep(300 * time.Millisecond)

	// Poll until we see the agent's comment on the task
	deadline := time.Now().Add(5 * time.Second)
	var agentComments []Comment
	for time.Now().Before(deadline) {
		agentComments = ms.GetCommentsForTask(taskID)
		// Filter for Ivy Agent's comments (skip the mention itself)
		count := 0
		for _, c := range agentComments {
			if c.User.Username == "Ivy Agent" {
				count++
			}
		}
		if count >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify Phase 1 results
	t.Log("--- Verifying Phase 1 ---")

	// 1. Reaction should have been added
	reactions := ms.GetReactions(parentCommentID)
	if len(reactions) == 0 || reactions[0] != "herb" {
		t.Fatalf("expected herb reaction on mention comment, got %v", reactions)
	}
	t.Log("✓ 🌿 reaction added to mention comment")

	// 2. Acknowledgment reply should exist
	replies := ms.GetRepliesForComment(parentCommentID)
	hasAck := false
	for _, r := range replies {
		if r.User.Username == "Ivy Agent" && strings.Contains(r.CommentText, "Looking into this") {
			hasAck = true
		}
	}
	if !hasAck {
		t.Fatal("expected acknowledgment reply '🌿 Looking into this now...'")
	}
	t.Log("✓ Acknowledgment reply posted")

	// 3. Agent should have posted an investigation comment on the task
	var investigationComment *Comment
	for i := range agentComments {
		c := agentComments[i]
		if c.User.Username == "Ivy Agent" && strings.Contains(c.CommentText, "Investigation Results") {
			investigationComment = &c
			break
		}
	}
	if investigationComment == nil {
		// Print all comments for debugging
		for _, c := range agentComments {
			t.Logf("  Comment from %s: %s", c.User.Username, c.CommentText[:min(100, len(c.CommentText))])
		}
		t.Fatal("expected investigation comment from Ivy Agent")
	}
	t.Logf("✓ Agent posted investigation comment: %.80s...", investigationComment.CommentText)

	// 4. Tool calls should have been made
	toolCalls := toolExec.getCalls()
	if len(toolCalls) < 2 {
		t.Fatalf("expected at least 2 tool calls (list_parser_hosts, exec_on_host), got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "list_parser_hosts" {
		t.Errorf("expected first tool call to be list_parser_hosts, got %s", toolCalls[0].Name)
	}
	if toolCalls[1].Name != "exec_on_host" {
		t.Errorf("expected second tool call to be exec_on_host, got %s", toolCalls[1].Name)
	}
	t.Logf("✓ Tool calls: %s, %s (and %d more)", toolCalls[0].Name, toolCalls[1].Name, len(toolCalls)-2)

	// 5. Session should be completed
	allSessions := sessions.sessions
	var firstSessionID string
	for id, s := range allSessions {
		if s.Source == "clickup" && s.SourceID == taskID {
			firstSessionID = id
			break
		}
	}
	if firstSessionID != "" {
		sess, _ := sessions.Get(ctx, firstSessionID)
		if sess.Status != model.StatusCompleted {
			t.Errorf("expected session to be completed, got %s", sess.Status)
		} else {
			t.Log("✓ First session completed")
		}
	}

	// ════════════════════════════════════════════════════════════
	// PHASE 2: User follows up in the thread
	// ════════════════════════════════════════════════════════════
	t.Log("")
	t.Log("=== PHASE 2: User follows up in thread ===")

	// Reset tool calls counter for phase 2
	phase1Calls := len(toolExec.getCalls())

	// User replies in the thread mentioning the agent
	threadReplyID := ms.PostThreadReply(parentCommentID, "Collin",
		"@Ivy Agent Would you mind fixing grok parse failures in a sandbox and testing your changes end to end?")
	t.Logf("Thread reply posted: %s", threadReplyID)

	// Wait for the poller to detect the thread mention and the agent to respond
	time.Sleep(500 * time.Millisecond)

	// Wait for a new reply in the thread from the agent
	deadline = time.Now().Add(10 * time.Second)
	var threadRepliesFromAgent []Comment
	for time.Now().Before(deadline) {
		allReplies := ms.GetRepliesForComment(parentCommentID)
		for _, r := range allReplies {
			if r.User.Username == "Ivy Agent" && !strings.Contains(r.CommentText, "Looking into this") {
				threadRepliesFromAgent = append(threadRepliesFromAgent, r)
			}
		}
		if len(threadRepliesFromAgent) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify Phase 2 results
	t.Log("--- Verifying Phase 2 ---")

	// 6. Agent should have replied in the thread with sandbox results
	if len(threadRepliesFromAgent) == 0 {
		allReplies := ms.GetRepliesForComment(parentCommentID)
		t.Logf("All thread replies (%d):", len(allReplies))
		for _, r := range allReplies {
			t.Logf("  [%s]: %s", r.User.Username, r.CommentText[:min(100, len(r.CommentText))])
		}
		t.Fatal("expected agent to reply in the thread with sandbox test results")
	}

	foundSandboxReply := false
	for _, r := range threadRepliesFromAgent {
		if strings.Contains(r.CommentText, "Sandbox Test Results") {
			foundSandboxReply = true
			t.Logf("✓ Agent replied in thread: %.80s...", r.CommentText)
			break
		}
	}
	if !foundSandboxReply {
		t.Errorf("expected thread reply to contain 'Sandbox Test Results', got: %v",
			threadRepliesFromAgent[0].CommentText[:min(100, len(threadRepliesFromAgent[0].CommentText))])
	}

	// 7. Phase 2 tool calls should include create_sandbox and pipeline_health
	phase2Calls := toolExec.getCalls()[phase1Calls:]
	hasCreateSandbox := false
	hasPipelineHealth := false
	for _, tc := range phase2Calls {
		if tc.Name == "create_sandbox" {
			hasCreateSandbox = true
		}
		if tc.Name == "pipeline_health" {
			hasPipelineHealth = true
		}
	}
	if !hasCreateSandbox {
		t.Error("expected create_sandbox tool call in phase 2")
	} else {
		t.Log("✓ create_sandbox called in phase 2")
	}
	if !hasPipelineHealth {
		t.Error("expected pipeline_health tool call in phase 2")
	} else {
		t.Log("✓ pipeline_health called in phase 2")
	}

	// 8. Phase 2 should use clickup_reply_comment, NOT clickup_post_comment
	hasReplyComment := false
	hasPostComment := false
	for _, tc := range phase2Calls {
		if tc.Name == "clickup_reply_comment" {
			hasReplyComment = true
		}
		if tc.Name == "clickup_post_comment" {
			hasPostComment = true
		}
	}
	if hasPostComment {
		t.Error("phase 2 should NOT use clickup_post_comment for thread replies")
	}
	if !hasReplyComment {
		t.Error("expected clickup_reply_comment tool call in phase 2")
	} else {
		t.Log("✓ Used clickup_reply_comment (not clickup_post_comment)")
	}

	t.Log("")
	t.Log("=== E2E test passed! ===")
}
