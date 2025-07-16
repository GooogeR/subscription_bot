package bot

import (
	"errors"
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

// Для механизма добавления подписки по номеру клиента
var addSubStep = make(map[int64]string)
var cachedUsers = make(map[int64][]models.User)
var selectedUserIndex = make(map[int64]int)

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

	// Запуск периодической проверки напоминаний о подписках
	go func() {
		for {
			checkSubscriptionReminders(bot, db)
			time.Sleep(24 * time.Hour)
		}
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("PANIC RECOVERED: %v", r)
				}
			}()

			if update.Message != nil {
				chatID := update.Message.Chat.ID
				userName := update.Message.From.UserName
				telegramID := update.Message.From.ID
				text := update.Message.Text

				// Обработка состояния добавления подписки
				if step, ok := addSubStep[telegramID]; ok && step == "awaiting_user_selection" {
					num, err := strconv.Atoi(text)
					if err != nil || num < 1 {
						msg := tgbotapi.NewMessage(chatID, "Введите корректный номер клиента.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
						return
					}

					users, ok := cachedUsers[telegramID]
					if !ok || num > len(users) {
						msg := tgbotapi.NewMessage(chatID, "Такого номера клиента нет.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
						return
					}

					selectedUserIndex[telegramID] = num - 1
					addSubStep[telegramID] = "awaiting_duration"

					keyboard := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("30 дней", "sub_add_30"),
							tgbotapi.NewInlineKeyboardButtonData("90 дней", "sub_add_90"),
						),
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("180 дней", "sub_add_180"),
							tgbotapi.NewInlineKeyboardButtonData("360 дней", "sub_add_360"),
						),
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить подписку", "sub_add_delete"),
						),
					)

					msg := tgbotapi.NewMessage(chatID, "Выберите действие с подпиской:")
					msg.ReplyMarkup = keyboard
					bot.Send(msg)
					return
				}

				// Остальная логика сообщений
				switch {
				case strings.HasPrefix(text, "/admin extendsub"):
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						handleAdminExtendSubCommand(bot, chatID, db, text)
					}

				case strings.HasPrefix(text, "/admin removesub"):
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						handleAdminRemoveSubCommand(bot, chatID, db, text)
					}

				case text == "/start":
					registerUser(db, telegramID, userName)
					msg := tgbotapi.NewMessage(chatID, "Ты зарегистрирован, Добро пожаловать!")
					msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
					bot.Send(msg)

				case text == "/clients":
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
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
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						handleUnbindCommand(bot, chatID, db, telegramID, text)
					}

				case text == "/admin users":
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						handleAdminUsersCommand(bot, chatID, db, adminTelegramID)
					}

				case strings.HasPrefix(text, "/admin addsub"):
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						handleAdminAddSubCommand(bot, chatID, db, text)
					}

				case strings.HasPrefix(text, "/admin genconf"):
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для выполнения этой команды.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						handleAdminGenConfCommand(bot, chatID, db, text)
					}

				default:
					msg := tgbotapi.NewMessage(chatID, "Неизвестная команда, напиши /start ")
					msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
					bot.Send(msg)
				}

			} else if update.CallbackQuery != nil {
				callback := update.CallbackQuery
				chatID := callback.Message.Chat.ID
				telegramID := callback.From.ID
				data := callback.Data

				_, err := bot.Request(tgbotapi.CallbackConfig{
					CallbackQueryID: callback.ID,
					Text:            "",
				})
				if err != nil {
					log.Printf("Error sending callback answer: %v", err)
				}

				switch data {
				case "sub_add_30", "sub_add_90", "sub_add_180", "sub_add_360":
					handleSubscriptionAdd(bot, db, telegramID, chatID, data)
				case "sub_add_delete":
					handleSubscriptionDelete(bot, db, telegramID, chatID)
				case "status":
					handleStatusCommand(bot, chatID, db, telegramID, adminTelegramID)
				case "clients":
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для просмотра клиентов.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						handleClientsCommand(bot, chatID, db)
					}
				case "bind":
					msg := tgbotapi.NewMessage(chatID, "Чтобы привязать устройство, отправьте команду в формате:\n/bind <ваш_public_key> <название устройства>")
					msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
					bot.Send(msg)
				case "unbind":
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ Только администратор может отвязывать устройства.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					} else {
						msg := tgbotapi.NewMessage(chatID, "Чтобы отвязать устройство, отправьте команду в формате:\n/unbind <номер устройства>")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
					}
				case "addsub":
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ Только администратор может добавлять подписки.")
						msg.ReplyMarkup = getMainMenuKeyboard(true)
						bot.Send(msg)
					} else {
						var users []models.User
						db.Find(&users)
						cachedUsers[telegramID] = users
						addSubStep[telegramID] = "awaiting_user_selection"

						textList := "Клиенты:\n"
						for i, u := range users {
							username := u.Username
							if username == "" {
								username = "(без username)"
							}
							textList += fmt.Sprintf("%d) @%s — ID: %d\n", i+1, username, u.TelegramID)
						}
						textList += "\nВведите номер клиента для изменения подписки."

						msg := tgbotapi.NewMessage(chatID, textList)
						msg.ReplyMarkup = getMainMenuKeyboard(true)
						bot.Send(msg)
					}
				default:
					msg := tgbotapi.NewMessage(chatID, "Неизвестная команда с кнопки.")
					msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
					bot.Send(msg)
				}
			}
		}()
	}
}

// --- Новые функции для подписок через кнопки ---
func handleSubscriptionAdd(bot *tgbotapi.BotAPI, db *gorm.DB, telegramID, chatID int64, data string) {
	log.Printf("[INFO] Обработка %s для telegramID=%d", data, telegramID)
	daysStr := strings.TrimPrefix(data, "sub_add_")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка: некорректный срок."))
		return
	}

	users, ok := cachedUsers[telegramID]
	if !ok || len(users) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Список клиентов не найден."))
		return
	}

	index, ok := selectedUserIndex[telegramID]
	if !ok || index < 0 || index >= len(users) {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Выбранный пользователь не найден."))
		return
	}

	user := users[index]
	var dbUser models.User
	if err := db.First(&dbUser, "telegram_id = ?", user.TelegramID).Error; err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Пользователь не найден в базе."))
		return
	}

	now := time.Now()
	newExpiry := now.AddDate(0, 0, days)

	var sub models.Subscription
	err = db.Where("user_id = ? AND expires_at > ?", dbUser.ID, now).
		Order("expires_at DESC").
		First(&sub).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		newSub := models.Subscription{
			UserID:    dbUser.ID,
			Title:     "Подписка",
			ExpiresAt: newExpiry,
			CreatedAt: now,
		}
		if err := db.Create(&newSub).Error; err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при создании подписки."))
			return
		}
	} else if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка базы данных."))
		return
	} else {
		sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, days)
		if err := db.Save(&sub).Error; err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при продлении подписки."))
			return
		}
		newExpiry = sub.ExpiresAt
	}

	delete(addSubStep, telegramID)
	delete(selectedUserIndex, telegramID)
	delete(cachedUsers, telegramID)

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"✅ Подписка пользователя @%s продлена на %d дней.\nНовая дата окончания: %s",
		dbUser.Username, days, newExpiry.Format("02-01-2006")))
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
}

func handleSubscriptionDelete(bot *tgbotapi.BotAPI, db *gorm.DB, telegramID, chatID int64) {
	log.Printf("[INFO] Обработка удаления подписки для telegramID=%d", telegramID)

	users, ok := cachedUsers[telegramID]
	if !ok || len(users) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Список клиентов не найден."))
		return
	}

	index, ok := selectedUserIndex[telegramID]
	if !ok || index < 0 || index >= len(users) {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Выбранный пользователь не найден."))
		return
	}

	user := users[index]
	var dbUser models.User
	if err := db.First(&dbUser, "telegram_id = ?", user.TelegramID).Error; err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Пользователь не найден в базе."))
		return
	}

	if err := db.Where("user_id = ?", dbUser.ID).Delete(&models.Subscription{}).Error; err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при удалении подписки."))
		return
	}

	delete(addSubStep, telegramID)
	delete(selectedUserIndex, telegramID)
	delete(cachedUsers, telegramID)

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("🗑️ Подписка пользователя @%s удалена.", dbUser.Username))
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
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
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	if len(users) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Пользователи не найдены.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
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
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
}

func handleStatusCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, telegramID int64, adminTelegramID int64) {
	var user models.User
	result := db.First(&user, "telegram_id = ?", telegramID)
	if result.Error != nil {
		msg := tgbotapi.NewMessage(chatID, "Вы ещё не зарегистрированы. Напишите /start")
		msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
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
	msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
	bot.Send(msg)
}

func handleBindCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, telegramID int64, text string) {
	parts := strings.Fields(text)
	if len(parts) < 3 {
		msg := tgbotapi.NewMessage(chatID, "Ошибка: укажите ключ и название устройства.\n\nФормат:\n/bind <ваш_public_key> <название устройства>")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	publicKey := parts[1]
	deviceName := strings.Join(parts[2:], " ")

	var user models.User
	result := db.First(&user, "telegram_id = ?", telegramID)
	if result.Error != nil {
		msg := tgbotapi.NewMessage(chatID, "Вы ещё не зарегистрированы. Напишите /start")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	var existing models.Device
	err := db.Where("user_id = ? AND public_key = ?", user.ID, publicKey).First(&existing).Error
	if err == nil {
		msg := tgbotapi.NewMessage(chatID, "⚠️ Устройство с этим ключом уже привязано.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
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
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
}

func handleUnbindCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, telegramID int64, text string) {
	// Эту функцию вызываем только для admin, проверка есть в RunBot
	parts := strings.Fields(text)
	if len(parts) < 2 {
		msg := tgbotapi.NewMessage(chatID, "Укажите номер устройства для отвязки.\nПример: /unbind 2")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	index, err := strconv.Atoi(parts[1])
	if err != nil || index < 1 {
		msg := tgbotapi.NewMessage(chatID, "Неверный номер устройства.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	var devices []models.Device
	db.Find(&devices)
	if index > len(devices) {
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Устройство с номером %d не найдено.", index))
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	device := devices[index-1]

	var user models.User
	db.First(&user, device.UserID)

	db.Delete(&device)

	msgText := fmt.Sprintf("✅ Устройство *%s* (PublicKey: `%s`) успешно отвязано у пользователя @%s", device.DeviceName, device.PublicKey, user.Username)
	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
}

func handleAdminAddSubCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, text string) {
	parts := strings.SplitN(text, " ", 5)
	if len(parts) < 5 {
		msg := tgbotapi.NewMessage(chatID, "Неверный формат команды.\nИспользование: /admin addsub <telegramID> <дни> <название подписки>")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	telegramIDStr := parts[2]
	daysStr := parts[3]
	title := parts[4]

	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Неверный Telegram ID. Он должен быть числом.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		msg := tgbotapi.NewMessage(chatID, "Неверное количество дней. Это должно быть положительное число.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	var user models.User
	result := db.First(&user, "telegram_id = ?", telegramID)
	if result.Error != nil {
		msg := tgbotapi.NewMessage(chatID, "Пользователь с таким Telegram ID не найден. Сначала попросите его написать /start боту.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
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
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
}

func handleAdminUsersCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, adminTelegramID int64) {
	var users []models.User
	db.Find(&users)

	if len(users) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Нет зарегистрированных пользователей.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
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
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
}

func handleAdminGenConfCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, text string) {
	// Формат: /admin genconf <telegramID> <название устройства>
	parts := strings.Fields(text)
	if len(parts) < 4 {
		msg := tgbotapi.NewMessage(chatID, "Формат: /admin genconf <telegramID> <название устройства>")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	tgID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Некорректный telegramID")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}
	deviceName := parts[3]

	// Найти пользователя
	var user models.User
	if err := db.First(&user, "telegram_id = ?", tgID).Error; err != nil {
		msg := tgbotapi.NewMessage(chatID, "Пользователь не найден")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	// Создать устройство через wg-easy
	conf, qr, err := wireguard.AddClientViaDocker(deviceName)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ Ошибка: %v", err))
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
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

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Конфиг для устройства *%s* создан и отправлен.", deviceName))
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
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
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	telegramID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Неверный Telegram ID.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	days, err := strconv.Atoi(parts[3])
	if err != nil || days <= 0 {
		msg := tgbotapi.NewMessage(chatID, "Количество дней должно быть положительным числом.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	var user models.User
	if err := db.First(&user, "telegram_id = ?", telegramID).Error; err != nil {
		msg := tgbotapi.NewMessage(chatID, "Пользователь не найден.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	var sub models.Subscription
	now := time.Now()
	if err := db.Where("user_id = ? AND expires_at > ?", user.ID, now).First(&sub).Error; err != nil {
		msg := tgbotapi.NewMessage(chatID, "Активная подписка не найдена.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, days)
	db.Save(&sub)

	msg := tgbotapi.NewMessage(chatID,
		fmt.Sprintf("✅ Подписка пользователя @%s продлена на %d дней. Новая дата: %s",
			user.Username, days, sub.ExpiresAt.Format("02.01.2006")),
	)
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)
}

func handleAdminRemoveSubCommand(bot *tgbotapi.BotAPI, chatID int64, db *gorm.DB, text string) {
	parts := strings.SplitN(text, " ", 3)
	if len(parts) < 3 {
		msg := tgbotapi.NewMessage(chatID, "Формат: /admin removesub <telegramID>")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	telegramID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Неверный Telegram ID.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	var user models.User
	if err := db.First(&user, "telegram_id = ?", telegramID).Error; err != nil {
		msg := tgbotapi.NewMessage(chatID, "Пользователь не найден.")
		msg.ReplyMarkup = getMainMenuKeyboard(true)
		bot.Send(msg)
		return
	}

	db.Where("user_id = ?", user.ID).Delete(&models.Subscription{})

	msg := tgbotapi.NewMessage(chatID,
		fmt.Sprintf("🗑️ Подписки пользователя @%s удалены.", user.Username),
	)
	msg.ReplyMarkup = getMainMenuKeyboard(true)
	bot.Send(msg)

}

// getMainMenuKeyboard возвращает основное меню в виде инлайн-клавиатуры
func getMainMenuKeyboard(isAdmin bool) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Статус", "status"),
			tgbotapi.NewInlineKeyboardButtonData("Клиенты", "clients"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Привязать устройство", "bind"),
			tgbotapi.NewInlineKeyboardButtonData("Отвязать устройство", "unbind"),
		),
	}
	if isAdmin {
		// Добавляем кнопку "Добавить подписку" только для админа
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Добавить подписку", "addsub"),
		))
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// checkSubscriptionReminders отправляет напоминания пользователям о скором окончании подписки
func checkSubscriptionReminders(bot *tgbotapi.BotAPI, db *gorm.DB) {
	now := time.Now()
	reminderDays := []int{1, 3, 7}

	for _, daysLeft := range reminderDays {
		checkTime := now.AddDate(0, 0, daysLeft)
		var subs []models.Subscription

		err := db.Preload("User").
			Where("DATE(expires_at) = ?", checkTime.Format("2006-01-02")).
			Find(&subs).Error
		if err != nil {
			log.Printf("Ошибка выборки подписок для напоминаний: %v", err)
			continue
		}

		for _, sub := range subs {
			if sub.User.TelegramID == 0 {
				log.Printf("Подписка %d не имеет связанного пользователя", sub.ID)
				continue
			}

			text := fmt.Sprintf("⏳ У вас заканчивается подписка через %d день(дней). Для продления напишите в личные сообщения @GooogeR", daysLeft)
			msg := tgbotapi.NewMessage(sub.User.TelegramID, text)
			if _, err := bot.Send(msg); err != nil {
				log.Printf("Ошибка отправки напоминания пользователю %d: %v", sub.User.TelegramID, err)
			}
		}
	}
}
