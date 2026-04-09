package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"time"

	"smartcontrol/internal/db"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type AuthAPI struct {
	db *db.DB
}

func RegisterAuthRoutes(r *gin.Engine, database *db.DB) {
	a := &AuthAPI{db: database}
	auth := r.Group("/api/auth")
	{
		auth.POST("/register", a.Register)
		auth.POST("/login", a.Login)
		auth.POST("/logout", a.Logout)
		auth.GET("/validate", a.ValidateToken)
	}
	// Start background cleanup of expired sessions
	go a.cleanupExpiredSessions()
}

func (a *AuthAPI) Register(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required,min=3"`
		Password string `json:"password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名至少3位，密码至少6位"})
		return
	}

	// Check if username exists
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, req.Username).Scan(&count)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "数据库错误"})
		return
	}
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}

	// Insert user
	_, err = a.db.Exec(`INSERT INTO users(username, password_hash) VALUES(?, ?)`, req.Username, string(hash))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *AuthAPI) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名和密码不能为空"})
		return
	}

	var passwordHash string
	err := a.db.QueryRow(`SELECT password_hash FROM users WHERE username = ?`, req.Username).Scan(&passwordHash)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "数据库错误"})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	// Get user ID
	var userID int
	err = a.db.QueryRow(`SELECT id FROM users WHERE username = ?`, req.Username).Scan(&userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "数据库错误"})
		return
	}

	// Generate token and save to database
	token := generateToken()
	expiresAt := time.Now().Add(24 * time.Hour) // 24 hours validity

	_, err = a.db.Exec(`INSERT INTO sessions(user_id, token, expires_at) VALUES(?, ?, ?)`,
		userID, token, expiresAt.Format("2006-01-02 15:04:05"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "会话创建失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":      token,
		"username":   req.Username,
		"expires_at": expiresAt.Unix(),
	})
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b) + "-" + time.Now().Format("20060102150405")
}

func (a *AuthAPI) Logout(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("token")
	}
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}

	// Remove Bearer prefix if present
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	_, err := a.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "登出失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *AuthAPI) ValidateToken(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("token")
	}
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false})
		return
	}

	// Remove Bearer prefix if present
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	var userID int
	var expiresAt string
	err := a.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token = ?`, token).Scan(&userID, &expiresAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": "invalid token"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"valid": false})
		return
	}

	// Check if token expired
	expires, err := time.Parse("2006-01-02 15:04:05", expiresAt)
	if err != nil || time.Now().After(expires) {
		// Delete expired token
		a.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": "token expired"})
		return
	}

	// Get username
	var username string
	err = a.db.QueryRow(`SELECT username FROM users WHERE id = ?`, userID).Scan(&username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"valid": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{"valid": true, "username": username, "expires_at": expires.Unix()})
}

func (a *AuthAPI) cleanupExpiredSessions() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE datetime(expires_at) < datetime('now')`)
	}
}
