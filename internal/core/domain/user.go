package domain

import "time"

type UserState string

const (
	UserStateActive     = "ACTIVE"
	UserStateTerminated = "TERMINATED"
)

type UserRole string

const (
	UserRoleAdmin = "ADMIN"
)

type User struct {
	Username  string
	Password  string
	State     UserState
	Role      UserRole
	CreatedAt time.Time
	UpdatedAt time.Time
}
