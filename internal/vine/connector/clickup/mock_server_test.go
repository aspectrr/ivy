package clickup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

func newTestMockServer(t *testing.T) (*MockServer, *Client) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ms := NewMockServer(logger)
	cfg := config.ClickUpConfig{
		TeamID:        "team1",
		AgentUsername: "Ivy Agent",
	}
	client := NewMockClient(ms, cfg, logger)
	return ms, client
}

func TestMockServerGetTasks(t *testing.T) {
	ms, client := newTestMockServer(t)
	defer ms.Close()

	// Add a mock task
	ms.AddTask(Task{
		ID:          "task1",
		Name:        "Test Task",
		Status:      Status{Status: "open"},
		DateUpdated: fmt.Sprintf("%d", time.Now().UnixMilli()),
	})

	tasks, err := client.GetTeamTasks(context.Background(), &TaskListOpts{})
	if err != nil {
		t.Fatalf("GetTeamTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "task1" {
		t.Fatalf("expected task1, got %s", tasks[0].ID)
	}
}

func TestMockServerGetComments(t *testing.T) {
	ms, client := newTestMockServer(t)
	defer ms.Close()

	// Post a mention comment via mock
	ms.PostMention("task1", "Collin", "Hey @Ivy Agent check this out")

	comments, err := client.GetComments(context.Background(), "task1")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].CommentText != "Hey @Ivy Agent check this out" {
		t.Fatalf("unexpected comment text: %s", comments[0].CommentText)
	}
	if comments[0].User.Username != "Collin" {
		t.Fatalf("unexpected author: %s", comments[0].User.Username)
	}
}

func TestMockServerPostComment(t *testing.T) {
	ms, client := newTestMockServer(t)
	defer ms.Close()

	ms.AddTask(Task{ID: "task1", Name: "Test"})

	comment, err := client.PostComment(context.Background(), "task1", "Here's my analysis")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if comment.CommentText != "Here's my analysis" {
		t.Fatalf("unexpected text: %s", comment.CommentText)
	}
	if comment.User.Username != "Ivy Agent" {
		t.Fatalf("expected Ivy Agent as author, got %s", comment.User.Username)
	}

	// Verify it shows up in comments
	comments, _ := client.GetComments(context.Background(), "task1")
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment on task, got %d", len(comments))
	}
}

func TestMockServerReplyToComment(t *testing.T) {
	ms, client := newTestMockServer(t)
	defer ms.Close()

	// Create a parent comment
	parentID := ms.PostMention("task1", "Collin", "Hey @Ivy Agent")

	// Agent replies in thread
	reply, err := client.ReplyToComment(context.Background(), json.Number(parentID), "🌿 Looking into this now...")
	if err != nil {
		t.Fatalf("ReplyToComment: %v", err)
	}
	if reply.CommentText != "🌿 Looking into this now..." {
		t.Fatalf("unexpected reply text: %s", reply.CommentText)
	}
	if reply.User.Username != "Ivy Agent" {
		t.Fatalf("expected Ivy Agent as author, got %s", reply.User.Username)
	}

	// Verify replies show up
	replies, err := client.GetCommentReplies(context.Background(), json.Number(parentID))
	if err != nil {
		t.Fatalf("GetCommentReplies: %v", err)
	}
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(replies))
	}
	if replies[0].CommentText != "🌿 Looking into this now..." {
		t.Fatalf("unexpected reply text: %s", replies[0].CommentText)
	}
}

func TestMockServerAddReaction(t *testing.T) {
	ms, client := newTestMockServer(t)
	defer ms.Close()

	commentID := ms.PostMention("task1", "Collin", "Hey @Ivy Agent")

	err := client.AddCommentReaction(context.Background(), json.Number(commentID), "herb")
	if err != nil {
		t.Fatalf("AddCommentReaction: %v", err)
	}

	reactions := ms.GetReactions(commentID)
	if len(reactions) != 1 || reactions[0] != "herb" {
		t.Fatalf("expected [herb], got %v", reactions)
	}
}

func TestMockServerThreadFlow(t *testing.T) {
	// Simulate a full mention → reaction → reply → follow-up thread flow
	ms, client := newTestMockServer(t)
	defer ms.Close()

	// 1. User mentions the agent
	parentID := ms.PostMention("task1", "Collin", "@Ivy Agent investigate the logs")
	if parentID == "" {
		t.Fatal("expected non-empty parent comment ID")
	}

	// 2. Agent reacts
	err := client.AddCommentReaction(context.Background(), json.Number(parentID), "herb")
	if err != nil {
		t.Fatalf("AddCommentReaction: %v", err)
	}

	// 3. Agent posts acknowledgment reply
	_, err = client.ReplyToComment(context.Background(), json.Number(parentID), "🌿 Looking into this now...")
	if err != nil {
		t.Fatalf("ReplyToComment: %v", err)
	}

	// 4. Agent does work, then posts final answer
	_, err = client.PostComment(context.Background(), "task1", "Found the issue: grok pattern mismatch")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}

	// 5. User follows up in thread
	ms.PostThreadReply(parentID, "Collin", "@Ivy Agent can you fix it in a sandbox?")

	// 6. Verify thread has 2 replies (ack + user follow-up)
	replies := ms.GetRepliesForComment(parentID)
	if len(replies) != 2 {
		t.Fatalf("expected 2 replies in thread, got %d", len(replies))
	}
	if replies[0].User.Username != "Ivy Agent" {
		t.Fatalf("first reply should be from Ivy Agent, got %s", replies[0].User.Username)
	}
	if replies[1].User.Username != "Collin" {
		t.Fatalf("second reply should be from Collin, got %s", replies[1].User.Username)
	}

	// 7. Verify task has the final answer comment
	comments := ms.GetCommentsForTask("task1")
	if len(comments) != 2 { // mention + final answer
		t.Fatalf("expected 2 comments on task, got %d", len(comments))
	}
	if comments[1].CommentText != "Found the issue: grok pattern mismatch" {
		t.Fatalf("unexpected final comment: %s", comments[1].CommentText)
	}
}
