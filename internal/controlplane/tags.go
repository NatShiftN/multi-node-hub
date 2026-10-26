package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

const (
	batchConcurrency  = 8
	batchDefaultLimit = 30 * time.Second
	batchMaxLimit     = 10 * time.Minute
)

type batchCommandRequest struct {
	Tags      []string        `json:"tags"`
	NodeIDs   []string        `json:"node_ids"`
	AllOnline bool            `json:"all_online"`
	Plugin    string          `json:"plugin"`
	Payload   json.RawMessage `json:"payload"`
	TimeoutMS int64           `json:"timeout_ms"`
}

type batchNodeResult struct {
	NodeID     string `json:"node_id"`
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

func (s *Server) handleListTags(c *gin.Context) {
	tags, err := s.store.ListTagCounts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list tags"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

func (s *Server) handleSetNodeTags(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	var request struct {
		Tags []string `json:"tags"`
	}
	if err := bindJSONLimited(c, &request, 32<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	normalized, err := store.NormalizeTags(request.Tags)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.store.SetNodeTags(nodeID, normalized); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save tags"})
		return
	}
	s.audit(c, "set_tags", nodeID, map[string]any{"tags": normalized})
	c.JSON(http.StatusOK, gin.H{"node_id": nodeID, "tags": normalized})
}

func uniqueBatchNodeIDs(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] || !protocol.ValidAgentID(value) {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func (s *Server) handleBatchCommand(c *gin.Context) {
	started := time.Now()
	var request batchCommandRequest
	if err := bindJSONLimited(c, &request, 2<<20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tags, err := store.NormalizeTags(request.Tags)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tags"})
		return
	}
	nodeIDs := uniqueBatchNodeIDs(request.NodeIDs)
	if !request.AllOnline && len(nodeIDs) == 0 && len(tags) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "select tags, node_ids or all_online"})
		return
	}
	role := c.GetString("admin_role")
	allowedValue, _ := c.Get("admin_allowed_tags")
	allowed, _ := allowedValue.([]string)
	if role == "operator" && !request.AllOnline && len(nodeIDs) == 0 && !tagsSubset(tags, allowed) {
		c.JSON(http.StatusForbidden, gin.H{"error": "batch tags exceed operator AllowedTags"})
		return
	}
	if !pluginNamePattern.MatchString(request.Plugin) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plugin"})
		return
	}
	if role == "operator" && isHighRiskPlugin(request.Plugin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "operator cannot run destructive plugins"})
		return
	}
	if role == "operator" && len(nodeIDs) > 0 {
		for _, nodeID := range nodeIDs {
			nodeTags, tagErr := s.store.ListNodeTags(nodeID)
			if tagErr != nil || !tagsIntersect(allowed, nodeTags) {
				c.JSON(http.StatusForbidden, gin.H{"error": "node_ids exceed operator AllowedTags"})
				return
			}
		}
	}
	payload := request.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payload must be valid JSON"})
		return
	}
	timeout := batchDefaultLimit
	if request.TimeoutMS > 0 {
		timeout = time.Duration(request.TimeoutMS) * time.Millisecond
		if timeout > batchMaxLimit {
			timeout = batchMaxLimit
		}
	}
	var nodes []store.Node
	switch {
	case len(nodeIDs) > 0:
		nodes, err = s.store.ListOnlineNodesByIDs(nodeIDs)
	case request.AllOnline:
		nodes, err = s.store.ListOnlineNodes()
	default:
		nodes, err = s.store.ListOnlineNodesWithAllTags(tags)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to match nodes"})
		return
	}
	if role == "operator" {
		filtered := make([]store.Node, 0, len(nodes))
		for _, node := range nodes {
			nodeTags, tagErr := s.store.ListNodeTags(node.NodeID)
			if tagErr == nil && tagsIntersect(allowed, nodeTags) {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}

	results := make([]batchNodeResult, len(nodes))
	sem := make(chan struct{}, batchConcurrency)
	var wait sync.WaitGroup
	for index, node := range nodes {
		index, node := index, node
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-c.Request.Context().Done():
				results[index] = batchNodeResult{NodeID: node.NodeID, Name: node.Name, Error: "request cancelled"}
				return
			}
			if _, online := s.hub.Get(node.NodeID); !online {
				results[index] = batchNodeResult{NodeID: node.NodeID, Name: node.Name, Error: "agent is not connected"}
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
			defer cancel()
			result, elapsed, err := s.hub.Execute(ctx, node.NodeID, request.Plugin, payload, timeout)
			entry := batchNodeResult{
				NodeID:     node.NodeID,
				Name:       node.Name,
				DurationMS: elapsed.Milliseconds(),
			}
			switch {
			case err != nil:
				entry.Error = err.Error()
			case result.Error != "":
				entry.Error = result.Error
			default:
				entry.Success = true
			}
			results[index] = entry
		}()
	}
	wait.Wait()

	success := make([]batchNodeResult, 0, len(results))
	failed := make([]batchNodeResult, 0, len(results))
	successIDs := make([]string, 0, len(results))
	failedIDs := make([]string, 0, len(results))
	for _, result := range results {
		if result.Success {
			success = append(success, result)
			successIDs = append(successIDs, result.NodeID)
		} else {
			failed = append(failed, result)
			failedIDs = append(failedIDs, result.NodeID)
		}
	}
	s.audit(c, "batch_command", "", map[string]any{
		"tags":        tags,
		"node_ids":    nodeIDs,
		"all_online":  request.AllOnline,
		"plugin":      request.Plugin,
		"matched":     len(nodes),
		"success_ids": successIDs,
		"failed_ids":  failedIDs,
	})
	c.JSON(http.StatusOK, gin.H{
		"matched":    len(nodes),
		"success":    success,
		"failed":     failed,
		"plugin":     request.Plugin,
		"tags":       tags,
		"all_online": request.AllOnline,
		"total_ms":   time.Since(started).Milliseconds(),
	})
}
