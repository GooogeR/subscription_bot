package bot

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"subscription_bot/internal/models"
	"subscription_bot/internal/wireguard"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

func RunBot(db *gorm.DB, adminTelegramID int64) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatalf("Telegram bot token не установлен")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Авторизован как: %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID
		userName := update.Message.From.UserName
		telegramID := update.Message.From.ID
		text := update.Message.Text

		switch {
		case strings.HasPrefix(text, "/admin extendsub"):
			if telegramID != adminTelegramID {
				msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
				bot.Send(msg)
			} else {
				handleAdminExtendSubCommand(bot, chatID, db, text)
			}

		case strings.HasPrefix(text, "/admin removesub"):
			if telegramID != adminTelegramID {
				msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
				bot.Send(msg)
			} else {
				handleAdminRemoveSubCommand(bot, chatID, db, text)
			}
		case text == "/start":
			registerUser(db, telegramID, userName)
			msg := tgbotapi.NewMessage(chatID, "Ты зарегистрирован, Добро пожаловать!")
			bot.Send(msg)

		case text == "/clients":
			if telegramID != adminTelegramID {
				msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
				bot.Send(msg)
			} else {
				handleClientsCommand(bot, chatID, db)
			}

		case text == "/status":
			handleStatusCommand(bot, chatID, db, telegramID, adminTelegramID)

		case strings.HasPrefix(text, "/bind"):
			handleBindCommand(bot, chatID, db, telegramID, text)

		case strings.HasPrefix(text, "/unbind"):
			if telegramID != adminTelegramID {
				msg := tgbotapi.NewMessage(chatID, "❌ Извините, эта команда доступна только администратору.")
				bot.Send(msg)
			} else {
				handleUnbindCommand(bot, chatID, db, telegramID, text)
			}

		case text == "/admin users":
			if telegramID != adminTelegramID {
				msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
				bot.Send(msg)
			} else {
				handleAdminUsersCommand(bot, chatID, db)
			}

		case strings.HasPrefix(text, "/admin addsub"):
			if telegramID != adminTelegramID {
				msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
				bot.Send(msg)
			} else {
				handleAdminAddSubCommand(bot, chatID, db, text)
			}

		case strings.HasPrefix(text, "/admin genconf"):
			if telegramID != adminTelegramID {
				msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
				bot.Send(msg)
			} else {
				handleAdminGenConfCommand(bot, chatID, db, text)
			}

		default:
			msg := tgbotapi.NewMessage(chatID, "Неизвестная команда, напиши /start, /clients, /status, /bind <твой_public_key> <название устройства>, /unbind или /admin addsub <telegramID> <дни> <название_подписки> (только для админа)")
			bot.Send(msg)
		}
	}
}

func registerUser(db *gorm.DB, telegramID int64, username string) {
	var user models.User

	result := db.First(&user, "telegram_id = ?", telegramID)
	if result.Error == gorm.ErrRecordNotFound {
		newUser := models.User{
			TelegramID: telegramID,
			Username:   username,
		}
		db.Create(&newUser)
		log.Println("Новый пользователь зарегистрирован: @" + username + "(" + strconv.FormatInt(telegramID, 10) + ")")
	}
}

// Новая версия функции - выводим пользователей из базы
func handleClientsCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB) {
	var users []models.User
	result := db.Find(&users)
	if result.Error != nil {
		msg := tgbotapi.NewMessage(chatID, "Ошибка при получении пользователей из базы данных.")
		bot.Send(msg)
		return
	}

	if len(users) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Пользователи не найдены.")
		bot.Send(msg)
		return
	}

	text := fmt.Sprintf("👥 Зарегистрированные пользователи (%d):\n\n", len(users))
	for i, user := range users {
		username := user.Username
		if username == "" {
			username = "(без username)"
		}

		// Получаем самую свежую активную подписку
		var sub models.Subscription
		db.Where("user_id = ? AND expires_at > ?", user.ID, time.Now()).
			Order("expires_at DESC").
			First(&sub)

		subInfo := "— без подписки"
		if sub.ID != 0 {
			subInfo = fmt.Sprintf("— до %s", sub.ExpiresAt.Format("02-01-2006"))
		}

		text += fmt.Sprintf("%d) @%s — ID: %d %s\n", i+1, username, user.TelegramID, subInfo)

		if i >= 49 {
			text += "... (показано первые 50)\n"
			break
		}
	}

	msg := tgbotapi.NewMessage(chatID, text)
	bot.Send(msg)
}

func handleStatusCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, telegramID int64, adminTelegramID int64) {
	var user models.User
	result := db.First(&user, "telegram_id = ?", telegramID)
	if result.Error != nil {
		msg := tgbotapi.NewMessage(chatID, "Вы ещё не зарегистрированы. Напишите /start")
		bot.Send(msg)
		return
	}

	var devices []models.Device
	db.Where("user_id = ?", user.ID).Find(&devices)

	var subscription models.Subscription
	db.Where("user_id = ? AND expires_at > ?", user.ID, time.Now()).
		Order("expires_at DESC").
		First(&subscription)

	statusText := ""

	if telegramID == adminTelegramID {
		statusText += "✅ Статус: Активен (неограниченная подписка)\n"
	} else if subscription.ID != 0 {
		statusText += fmt.Sprintf("✅ Статус: Активен\n⏳ Подписка до: %s\n", subscription.ExpiresAt.Format("02-01-2006"))
	} else {
		statusText += "⚠️ Подписка не найдена или просрочена\n"
	}

	if len(devices) == 0 {
		statusText += "🔐 Устройства не привязаны.\n"
	} else {
		statusText += fmt.Sprintf("📱 Устройства (%d):\n", len(devices))
		for i, d := range devices {
			statusText += fmt.Sprintf("%d) *%s*\n`%s`\n", i+1, d.DeviceName, d.PublicKey)
		}
	}

	msg := tgbotapi.NewMessage(chatID, statusText)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func handleBindCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, telegramID int64, text string) {
	parts := strings.Fields(text)
	if len(parts) < 3 {
		msg := tgbotapi.NewMessage(chatID, "Ошибка: укажите ключ и название устройства.\n\nФормат:\n/bind <ваш_public_key> <название устройства>")
		bot.Send(msg)
		return
	}

	publicKey := parts[1]
	deviceName := strings.Join(parts[2:], " ")

	var user models.User
	result := db.First(&user, "telegram_id = ?", telegramID)
	if result.Error != nil {
		msg := tgbotapi.NewMessage(chatID, "Вы ещё не зарегистрированы. Напишите /start")
		bot.Send(msg)
		return
	}

	var existing models.Device
	err := db.Where("user_id = ? AND public_key = ?", user.ID, publicKey).First(&existing).Error
	if err == nil {
		msg := tgbotapi.NewMessage(chatID, "⚠️ Устройство с этим ключом уже привязано.")
		bot.Send(msg)
		return
	}

	device := models.Device{
		UserID:     user.ID,
		PublicKey:  publicKey,
		DeviceName: deviceName,
		CreatedAt:  time.Now(),
	}
	db.Create(&device)

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Устройство *%s* успешно привязано!\n🔐 Public Key: `%s`", deviceName, publicKey))
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func handleUnbindCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, telegramID int64, text string) {
	// Эту функцию вызываем только для admin, проверка есть в RunBot
	parts := strings.Fields(text)
	if len(parts) < 2 {
		bot.Send(tgbotapi.NewMessage(chatID, "Укажите номер устройства для отвязки.\nПример: /unbind 2"))
		return
	}

	index, err := strconv.Atoi(parts[1])
	if err != nil || index < 1 {
		bot.Send(tgbotapi.NewMessage(chatID, "Неверный номер устройства."))
		return
	}

	var devices []models.Device
	db.Find(&devices)
	if index > len(devices) {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Устройство с номером %d не найдено.", index)))
		return
	}

	device := devices[index-1]

	var user models.User
	db.First(&user, device.UserID)

	db.Delete(&device)

	msgText := fmt.Sprintf("✅ Устройство *%s* (PublicKey: `%s`) успешно отвязано у пользователя @%s", device.DeviceName, device.PublicKey, user.Username)
	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func handleAdminAddSubCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, text string) {
	parts := strings.SplitN(text, " ", 5)
	if len(parts) < 5 {
		msg := tgbotapi.NewMessage(chatID, "Неверный формат команды.\nИспользование: /admin addsub <telegramID> <дни> <название подписки>")
		bot.Send(msg)
		return
	}

	telegramIDStr := parts[2]
	daysStr := parts[3]
	title := parts[4]

	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Неверный Telegram ID. Он должен быть числом.")
		bot.Send(msg)
		return
	}

	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		msg := tgbotapi.NewMessage(chatID, "Неверное количество дней. Это должно быть положительное число.")
		bot.Send(msg)
		return
	}

	var user models.User
	result := db.First(&user, "telegram_id = ?", telegramID)
	if result.Error != nil {
		msg := tgbotapi.NewMessage(chatID, "Пользователь с таким Telegram ID не найден. Сначала попросите его написать /start боту.")
		bot.Send(msg)
		return
	}

	newSub := models.Subscription{
		UserID:    user.ID,
		Title:     title,
		ExpiresAt: time.Now().AddDate(0, 0, days),
		CreatedAt: time.Now(),
	}
	db.Create(&newSub)

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Подписка '%s' добавлена пользователю @%s (ID: %d) на %d дней.", title, user.Username, telegramID, days))
	bot.Send(msg)
}

func handleAdminUsersCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB) {
	var users []models.User
	db.Find(&users)

	if len(users) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Нет зарегистрированных пользователей.")
		bot.Send(msg)
		return
	}

	text := fmt.Sprintf("👥 Список пользователей (%d):\n\n", len(users))
	for i, user := range users {
		username := user.Username
		if username == "" {
			username = "(без username)"
		}
		text += fmt.Sprintf("%d) @%s — %d\n", i+1, username, user.TelegramID)
		if i >= 49 {
			text += "... (показано первые 50)\n"
			break
		}
	}

	msg := tgbotapi.NewMessage(chatID, text)
	bot.Send(msg)
}

func handleAdminGenConfCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, text string) {
	// Формат: /admin genconf <telegramID> <название устройства>
	parts := strings.Fields(text)
	if len(parts) < 4 {
		bot.Send(tgbotapi.NewMessage(chatID, "Формат: /admin genconf <telegramID> <название устройства>"))
		return
	}

	tgID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Некорректный telegramID"))
		return
	}
	deviceName := parts[3]

	// Найти пользователя
	var user models.User
	if err := db.First(&user, "telegram_id = ?", tgID).Error; err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Пользователь не найден"))
		return
	}

	// Создать устройство через wg-easy
	conf, qr, err := wireguard.AddClientViaDocker(deviceName)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ Ошибка: %v", err)))
		return
	}

	// Добавить устройство в БД
	device := models.Device{
		UserID:     user.ID,
		PublicKey:  extractPublicKeyFromConf(conf),
		DeviceName: deviceName,
		CreatedAt:  time.Now(),
	}
	db.Create(&device)

	// Отправить конфиг
	bot.Send(tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{
		Name:  deviceName + ".conf",
		Bytes: []byte(conf),
	}))

	// Отправить QR
	bot.Send(tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  deviceName + ".png",
		Bytes: qr,
	}))

	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Конфиг для устройства *%s* создан и отправлен.", deviceName)))
}

func extractPublicKeyFromConf(conf string) string {
	lines := strings.Split(conf, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "PublicKey") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func handleAdminExtendSubCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, text string) {
	parts := strings.SplitN(text, " ", 4)
	if len(parts) < 4 {
		msg := tgbotapi.NewMessage(chatID, "Формат: /admin extendsub <telegramID> <дни>")
		bot.Send(msg)
		return
	}

	telegramID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Неверный Telegram ID.")
		bot.Send(msg)
		return
	}

	days, err := strconv.Atoi(parts[3])
	if err != nil || days <= 0 {
		msg := tgbotapi.NewMessage(chatID, "Количество дней должно быть положительным числом.")
		bot.Send(msg)
		return
	}

	var user models.User
	if err := db.First(&user, "telegram_id = ?", telegramID).Error; err != nil {
		msg := tgbotapi.NewMessage(chatID, "Пользователь не найден.")
		bot.Send(msg)
		return
	}

	var sub models.Subscription
	now := time.Now()
	if err := db.Where("user_id = ? AND expires_at > ?", user.ID, now).First(&sub).Error; err != nil {
		msg := tgbotapi.NewMessage(chatID, "Активная подписка не найдена.")
		bot.Send(msg)
		return
	}

	sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, days)
	db.Save(&sub)

	msg := tgbotapi.NewMessage(chatID,
		fmt.Sprintf("✅ Подписка пользователя @%s продлена на %d дней. Новая дата: %s",
			user.Username, days, sub.ExpiresAt.Format("02.01.2006")),
	)
	bot.Send(msg)
}

func handleAdminRemoveSubCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, text string) {
	parts := strings.SplitN(text, " ", 3)
	if len(parts) < 3 {
		msg := tgbotapi.NewMessage(chatID, "Формат: /admin removesub <telegramID>")
		bot.Send(msg)
		return
	}

	telegramID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Неверный Telegram ID.")
		bot.Send(msg)
		return
	}

	var user models.User
	if err := db.First(&user, "telegram_id = ?", telegramID).Error; err != nil {
		msg := tgbotapi.NewMessage(chatID, "Пользователь не найден.")
		bot.Send(msg)
		return
	}

	db.Where("user_id = ?", user.ID).Delete(&models.Subscription{})

	msg := tgbotapi.NewMessage(chatID,
		fmt.Sprintf("🗑️ Подписки пользователя @%s удалены.", user.Username),
	)
	bot.Send(msg)
}
