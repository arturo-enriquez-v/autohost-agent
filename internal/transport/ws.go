// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"autohost-agent/internal/dispatcher"
	"autohost-agent/internal/domain"

	"github.com/gorilla/websocket"
)

// WSClient handles WebSocket communication with the API for receiving jobs.
type WSClient struct {
	url      string
	token    string
	conn     *websocket.Conn
	mu       sync.Mutex
	registry *dispatcher.Registry
}

// NewWSClient creates a new WebSocket client.
func NewWSClient(url, token string, registry *dispatcher.Registry) *WSClient {
	return &WSClient{
		url:      url,
		token:    token,
		registry: registry,
	}
}

// Connect establishes and maintains a WebSocket connection with automatic reconnection.
func (c *WSClient) Connect(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := c.connectOnce(ctx); err != nil {
				log.Printf("WebSocket connection failed: %v, retrying in 10s...", err)
				time.Sleep(10 * time.Second)
				continue
			}
		}
	}
}

func (c *WSClient) connectOnce(ctx context.Context) error {
	headers := http.Header{}
	headers.Add("Authorization", "Bearer "+c.token)

	log.Printf("Connecting to WebSocket: %s", c.url)
	conn, _, err := websocket.DefaultDialer.Dial(c.url, headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	log.Println("WebSocket connected successfully")

	if err := c.sendIdentification(); err != nil {
		log.Printf("Failed to send identification: %v", err)
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("WebSocket read error: %v", err)
				return err
			}
			go c.handleMessage(ctx, message)
		}
	}
}

func (c *WSClient) sendIdentification() error {
	msg := map[string]any{
		"type": "identify",
		"data": map[string]string{"version": "1.0.0"},
	}
	return c.send(msg)
}

func (c *WSClient) handleMessage(ctx context.Context, message []byte) {
	log.Printf("Received message: %s", string(message))

	var job domain.Job
	if err := json.Unmarshal(message, &job); err != nil {
		log.Printf("Failed to unmarshal job: %v", err)
		return
	}

	job.Status = domain.JobStatusRunning

	res := c.registry.Execute(ctx, job.Type, job.Payload)
	if res.Err != nil {
		log.Printf("Job execution failed: %v", res.Err)
		job.Status = domain.JobStatusFailed
		c.sendJobResult(&job, domain.JobStatusFailed, res.Err.Error())
	} else {
		log.Printf("Job completed successfully: %s", job.ID)
		job.Status = domain.JobStatusCompleted
		c.sendJobResult(&job, domain.JobStatusCompleted, "")
	}
}

func (c *WSClient) sendJobResult(job *domain.Job, status, errorMsg string) {
	result := map[string]any{
		"type":         "job_result",
		"job_id":       job.ID,
		"status":       status,
		"error":        errorMsg,
		"completed_at": time.Now().Unix(),
	}
	if err := c.send(result); err != nil {
		log.Printf("Failed to send job result: %v", err)
	}
}

func (c *WSClient) send(msg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.WriteJSON(msg)
}

// Close closes the WebSocket connection.
func (c *WSClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
