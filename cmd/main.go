package main

import (
	"log"
	"os"
	"strconv"
	"subscription_bot/internal/bot"

	"github.com/joho/godotenv"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	// Загружаем переменные из .env
	err := godotenv.Load(".env")
	if err != nil {
		log.Println("⚠️ Не удалось загрузить .env файл")
	}

	adminIDStr := os.Getenv("ADMIN_TELEGRAM_ID")
	if adminIDStr == "" {
		log.Fatal("Переменная окружения ADMIN_TELEGRAM_ID не установлена")
	}

	adminTelegramID, err := strconv.ParseInt(adminIDStr, 10, 64)
	if err != nil {
		log.Fatal("ADMIN_TELEGRAM_ID должен быть числом")
	}

	db, err := gorm.Open(sqlite.Open("subscription_bot.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("Не удалось подключиться к базе данных:", err)
	}

	bot.RunBot(db, adminTelegramID)
}
