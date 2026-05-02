package model

import "time"

type User struct {
	UserID    string    `json:"userId"`
	TenantID  string    `json:"tenantId"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
