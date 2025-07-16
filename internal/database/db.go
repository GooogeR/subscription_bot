package database

import (
	"subscription_bot/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func InitDB() (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open("subscription_bot.db"), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(&models.User{}, &models.Subscription{}, &models.Device{}); err != nil {
		return nil, err
	}

	return db, nil
}
