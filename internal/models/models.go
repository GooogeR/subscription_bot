package models

import "time"

type User struct {
	ID          int64 `gorm:"primaryKey"`
	TelegramID  int64 `gorm:"column:telegram_id"`
	Username    string
	WGPublicKey string
	CreatedAt   time.Time
}

type Subscription struct {
	ID        int64 `gorm:"primaryKey"`
	UserID    int64
	Title     string
	ExpiresAt time.Time
	CreatedAt time.Time

	User User `gorm:"foreignKey:UserID"`
}

type Device struct {
	ID         int64  `gorm:"primaryKey"`
	UserID     int64  `gorm:"index"`
	PublicKey  string `gorm:"uniqueIndex"`
	DeviceName string
	CreatedAt  time.Time

	User User `gorm:"foreignKey:UserID"`
}

// Добавляем WGClient для хранения данных клиента WireGuard
type WGClient struct {
	PublicKey  string
	AllowedIPs string
}
