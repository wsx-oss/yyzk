package handlers

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// NotificationAPI handles notification center endpoints
type NotificationAPI struct {
	db *sql.DB
}

// NewNotificationAPI creates a new NotificationAPI
func NewNotificationAPI(db *sql.DB) *NotificationAPI {
	return &NotificationAPI{db: db}
}

// NotificationList returns notifications with optional filters
func (n *NotificationAPI) NotificationList(c *gin.Context) {
	typ := strings.TrimSpace(c.Query("type"))
	readFilter := strings.TrimSpace(c.Query("is_read"))
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	q := "SELECT id, type, title, message, source, link, is_read, created_at FROM notifications WHERE 1=1"
	var args []any

	if typ != "" {
		q += " AND type = ?"
		args = append(args, typ)
	}
	if readFilter == "0" || readFilter == "1" {
		q += " AND is_read = ?"
		args = append(args, readFilter)
	}
	q += " ORDER BY datetime(created_at) DESC LIMIT ?"
	args = append(args, limit)

	rows, err := n.db.Query(q, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, isRead int
		var nType, title, message, source, link string
		var createdAt sql.NullString
		if err := rows.Scan(&id, &nType, &title, &message, &source, &link, &isRead, &createdAt); err == nil {
			items = append(items, gin.H{
				"id":         id,
				"type":       nType,
				"title":      title,
				"message":    message,
				"source":     source,
				"link":       link,
				"is_read":    isRead == 1,
				"created_at": createdAt.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// NotificationUnreadCount returns the count of unread notifications
func (n *NotificationAPI) NotificationUnreadCount(c *gin.Context) {
	var count int
	err := n.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE is_read = 0").Scan(&count)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"count": count})
}

// NotificationMarkRead marks a single notification as read
func (n *NotificationAPI) NotificationMarkRead(c *gin.Context) {
	id := c.Param("id")
	_, err := n.db.Exec("UPDATE notifications SET is_read = 1 WHERE id = ?", id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// NotificationMarkAllRead marks all notifications as read
func (n *NotificationAPI) NotificationMarkAllRead(c *gin.Context) {
	_, err := n.db.Exec("UPDATE notifications SET is_read = 1 WHERE is_read = 0")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// NotificationClearOld removes notifications older than 7 days that are already read
func (n *NotificationAPI) NotificationClearOld(c *gin.Context) {
	res, err := n.db.Exec("DELETE FROM notifications WHERE is_read = 1 AND datetime(created_at) < datetime('now', '-7 days')")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := res.RowsAffected()
	c.JSON(200, gin.H{"ok": true, "deleted": affected})
}

// NotificationCreate creates a notification (used internally or via API)
func (n *NotificationAPI) NotificationCreate(c *gin.Context) {
	var req struct {
		Type    string `json:"type"`
		Title   string `json:"title"`
		Message string `json:"message"`
		Source  string `json:"source"`
		Link    string `json:"link"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	if req.Type == "" {
		req.Type = "system"
	}
	if req.Title == "" {
		c.JSON(400, gin.H{"error": "title is required"})
		return
	}
	res, err := n.db.Exec(
		"INSERT INTO notifications(type, title, message, source, link) VALUES(?,?,?,?,?)",
		req.Type, req.Title, req.Message, req.Source, req.Link,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": id})
}

// RegisterNotificationRoutes registers all notification routes
func RegisterNotificationRoutes(r *gin.Engine, db *sql.DB) {
	api := NewNotificationAPI(db)
	g := r.Group("/api/notifications")
	{
		g.GET("", api.NotificationList)
		g.GET("/unread-count", api.NotificationUnreadCount)
		g.POST("/create", api.NotificationCreate)
		g.POST("/:id/read", api.NotificationMarkRead)
		g.POST("/read-all", api.NotificationMarkAllRead)
		g.POST("/clear-old", api.NotificationClearOld)
	}
}
