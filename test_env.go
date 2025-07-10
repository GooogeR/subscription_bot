package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Ошибка загрузки .env:", err)
	}
	fmt.Println("ADMIN_TELEGRAM_ID =", os.Getenv("ADMIN_TELEGRAM_ID"))
	fmt.Println("TELEGRAM_BOT_TOKEN =", os.Getenv("TELEGRAM_BOT_TOKEN"))
}
