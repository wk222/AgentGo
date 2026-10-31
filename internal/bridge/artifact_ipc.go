package bridge

import (
	"context"
	"strings"
	"time"

	"agentgo/internal/artifact"
)

// 对话里交付的文件（deliver_file）：列出当前状态、让用户把某个文件从交付物里移除。
// 记录和下载副本的归属见 core.Transcript.ArtifactStore。

// ListSessionArtifacts 返回一条对话里每个已交付文件的最新记录（含已被用户移除的，标 status=removed）。
func (s *AppService) ListSessionArtifacts(sessionID string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return map[string]any{"success": false, "error": "empty session_id"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recs, err := s.transcript().ListArtifacts(ctx, sessionID)
	if err != nil {
		return map[string]any{"success": false, "error": "读取交付记录失败：" + err.Error()}
	}
	if recs == nil {
		recs = []artifact.Record{}
	}
	return map[string]any{"success": true, "artifacts": recs}
}

// RemoveArtifact 把一个已交付的文件从交付物里移除：删掉下载副本并留下“已移除”记录。
// 之后内容不变的同一文件不会被再次交付；内容变了则算新版本。磁盘上的原文件不动。
func (s *AppService) RemoveArtifact(sessionID, artifactID string) map[string]any {
	sessionID, artifactID = strings.TrimSpace(sessionID), strings.TrimSpace(artifactID)
	if sessionID == "" || artifactID == "" {
		return map[string]any{"success": false, "error": "session_id and artifact_id are required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, found, err := s.transcript().RemoveArtifact(ctx, sessionID, artifactID)
	switch {
	case err != nil:
		return map[string]any{"success": false, "error": "移除失败：" + err.Error()}
	case !found:
		return map[string]any{"success": false, "error": "没有找到这个交付物"}
	}
	return map[string]any{"success": true, "artifact": rec}
}
