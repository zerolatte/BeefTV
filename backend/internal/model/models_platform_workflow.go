//go:build platform

package model

import "time"

// PlatformWorkflowRequest 把工作流重试标识绑定到工作区和原运行，步骤准入收据仍由点数事务管理。
type PlatformWorkflowRequest struct {
	ID          string `gorm:"primaryKey;size:36"`
	WorkspaceID string `gorm:"not null;uniqueIndex:idx_platform_workflow_request,priority:1;size:36"`
	Kind        string `gorm:"not null;uniqueIndex:idx_platform_workflow_request,priority:2;size:32"`
	RequestKey  string `gorm:"not null;uniqueIndex:idx_platform_workflow_request,priority:3;size:128"`
	RunID       string `gorm:"not null;index;size:36"`
	CreatedAt   time.Time
}

func (PlatformWorkflowRequest) TableName() string { return "platform_workflow_requests" }
