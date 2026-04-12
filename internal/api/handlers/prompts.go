package handlers

import (
	"text/template"

	"github.com/gin-gonic/gin"

	"github.com/fixloop/fixloop/internal/api/response"
	"github.com/fixloop/fixloop/internal/agents/fix"
	"github.com/fixloop/fixloop/internal/agents/plan"
	"github.com/fixloop/fixloop/internal/tgbot"
)

// GetPromptDefaults returns the built-in default prompt templates for all agents.
func (h *ProjectHandler) GetPromptDefaults(c *gin.Context) {
	response.OK(c, gin.H{
		"fix":            fix.DefaultPrompt(),
		"plan":           plan.DefaultPrompt(),
		"issue_analysis": tgbot.DefaultIssueAnalysisPrompt(),
	})
}

// ValidatePrompt checks whether the given Go text/template string parses without error.
func (h *ProjectHandler) ValidatePrompt(c *gin.Context) {
	var req struct {
		Template string `json:"template" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if _, err := template.New("validate").Parse(req.Template); err != nil {
		response.OK(c, gin.H{"valid": false, "error": err.Error()})
		return
	}
	response.OK(c, gin.H{"valid": true})
}
