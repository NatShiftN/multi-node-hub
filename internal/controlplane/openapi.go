package controlplane

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleOpenAPISpec serves the merchant-facing OpenAPI 3.0 document. The
// document is public (it contains no secrets); the /v1 endpoints it describes
// require an sk-... Bearer key.
func (s *Server) handleOpenAPISpec(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"openapi": "3.0.3",
		"info": gin.H{
			"title":       "NodeHub To-B OpenAPI",
			"description": "Merchant API for node inventory, traffic allocation and device limits. Use an sk-... API key as a Bearer token.",
			"version":     "0.9.8",
		},
		"servers":  []gin.H{{"url": "/"}},
		"security": []gin.H{{"bearerAuth": []string{}}},
		"components": gin.H{
			"securitySchemes": gin.H{
				"bearerAuth": gin.H{"type": "http", "scheme": "bearer", "bearerFormat": "sk-..."},
			},
			"schemas": gin.H{
				"Node": gin.H{
					"type": "object",
					"properties": gin.H{
						"id":            gin.H{"type": "string"},
						"name":          gin.H{"type": "string"},
						"address":       gin.H{"type": "string"},
						"remote_ip":     gin.H{"type": "string"},
						"status":        gin.H{"type": "string", "enum": []string{"online", "offline", "provisioning", "offline_network_error", "gfw_blocked"}},
						"agent_version": gin.H{"type": "string"},
						"os":            gin.H{"type": "string"},
						"arch":          gin.H{"type": "string"},
					},
				},
				"TrafficAllocateRequest": gin.H{
					"type":     "object",
					"required": []string{"email", "quota_bytes"},
					"properties": gin.H{
						"email":        gin.H{"type": "string", "format": "email"},
						"quota_bytes":  gin.H{"type": "integer", "format": "int64", "minimum": 0, "description": "0 means unlimited"},
						"device_limit": gin.H{"type": "integer", "minimum": 0, "maximum": 1000, "description": "0 means unlimited; stored as merchant policy (edge enforcement is a separate layer)"},
						"node_ids":     gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "Optional node scope for reset_usage"},
						"reset_usage":  gin.H{"type": "boolean", "default": false},
					},
				},
				"TrafficUser": gin.H{
					"type": "object",
					"properties": gin.H{
						"email":          gin.H{"type": "string"},
						"quota_bytes":    gin.H{"type": "integer", "format": "int64"},
						"device_limit":   gin.H{"type": "integer"},
						"is_banned":      gin.H{"type": "boolean"},
						"upload_bytes":   gin.H{"type": "integer", "format": "int64"},
						"download_bytes": gin.H{"type": "integer", "format": "int64"},
						"total_bytes":    gin.H{"type": "integer", "format": "int64"},
					},
				},
			},
		},
		"paths": gin.H{
			"/v1/openapi.json": gin.H{
				"get": gin.H{"summary": "This document", "responses": gin.H{"200": gin.H{"description": "OpenAPI document"}}},
			},
			"/v1/nodes": gin.H{
				"get": gin.H{
					"summary":   "List nodes",
					"security":  []gin.H{{"bearerAuth": []string{}}},
					"responses": gin.H{"200": gin.H{"description": "Node list", "content": gin.H{"application/json": gin.H{"schema": gin.H{"type": "object", "properties": gin.H{"nodes": gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/Node"}}}}}}}},
				},
			},
			"/v1/nodes/{id}": gin.H{
				"get": gin.H{
					"summary":    "Get node",
					"security":   []gin.H{{"bearerAuth": []string{}}},
					"parameters": []gin.H{{"name": "id", "in": "path", "required": true, "schema": gin.H{"type": "string"}}},
					"responses":  gin.H{"200": gin.H{"description": "Node"}, "404": gin.H{"description": "Not found"}},
				},
			},
			"/v1/traffic/allocate": gin.H{
				"post": gin.H{
					"summary":     "Allocate traffic quota and device limit",
					"security":    []gin.H{{"bearerAuth": []string{}}},
					"requestBody": gin.H{"required": true, "content": gin.H{"application/json": gin.H{"schema": gin.H{"$ref": "#/components/schemas/TrafficAllocateRequest"}}}},
					"responses":   gin.H{"200": gin.H{"description": "Allocated"}, "400": gin.H{"description": "Invalid request"}},
				},
			},
			"/v1/traffic/users/{email}": gin.H{
				"get": gin.H{
					"summary":    "Get traffic user",
					"security":   []gin.H{{"bearerAuth": []string{}}},
					"parameters": []gin.H{{"name": "email", "in": "path", "required": true, "schema": gin.H{"type": "string"}}},
					"responses":  gin.H{"200": gin.H{"description": "Traffic user", "content": gin.H{"application/json": gin.H{"schema": gin.H{"$ref": "#/components/schemas/TrafficUser"}}}}, "404": gin.H{"description": "Not found"}},
				},
			},
		},
	})
}
