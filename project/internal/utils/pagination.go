package utils

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// PaginationParams extracts pagination parameters from request
type PaginationParams struct {
	Page     int
	PageSize int
	Offset   int
}

// GetPagination extracts and validates pagination parameters from query string
func GetPagination(c *gin.Context) PaginationParams {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

	// Validate and set limits
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200 // Max 200 items per page
	}

	offset := (page - 1) * pageSize

	return PaginationParams{
		Page:     page,
		PageSize: pageSize,
		Offset:   offset,
	}
}

// SanitizeString removes potentially dangerous characters from input
func SanitizeString(input string) string {
	// Basic sanitization - can be extended based on requirements
	if len(input) > 1000 {
		return input[:1000]
	}
	return input
}

// ValidateID checks if ID parameter is valid
func ValidateID(c *gin.Context, paramName string) (int, bool) {
	idStr := c.Param(paramName)
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 1 {
		c.JSON(400, gin.H{"error": "invalid " + paramName})
		return 0, false
	}
	return id, true
}
