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

	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

var adminTelegramID int64
var addSubStep = make(map[int64]string)
var cachedUsers = make(map[int64][]models.User)
var selectedUserIndex = make(map[int64]int)

var bindStep = make(map[int64]string)
var pendingBindKey = make(map[int64]string)
var unbindStep = make(map[int64]bool)
var cachedUserDevices = make(map[int64][]models.Device)
var unlimitedUsers = make(map[int64]bool)

func RunBot(db *gorm.DB, adminTelegramID int64) {
	err := godotenv.Load()
	if err != nil {
		log.Println("⚠️ Не удалось загрузить .env файл, используем переменные окружения.")
	}
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatalf("Telegram bot token не установлен")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	unlimitedUsers = parseUnlimitedUsers(os.Getenv("UNLIMITED_USERS"))
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

	c := cron.New(cron.WithLocation(time.FixedZone("MSK", 3*60*60))) // МСК
	c.AddFunc("0 13 * * *", func() {
		checkSubscriptionReminders(bot, db)
	})
	c.Start()

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

				// --- Добавленная логика для отвязки устройства ---
				if unbindStep[telegramID] {
					index, err := strconv.Atoi(strings.TrimSpace(text))
					if err != nil || index < 1 {
						bot.Send(tgbotapi.NewMessage(chatID, "❌ Пожалуйста, введите корректный номер устройства."))
						return
					}

					devices, ok := cachedUserDevices[telegramID]
					if !ok || index > len(devices) {
						bot.Send(tgbotapi.NewMessage(chatID, "❌ Устройство с таким номером не найдено."))
						return
					}

					device := devices[index-1]

					if err := db.Delete(&device).Error; err != nil {
						bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при отвязке устройства. Попробуйте позже."))
						return
					}
					msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Устройство *%s* успешно отвязано.", device.DeviceName))
					msg.ParseMode = "Markdown"
					msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
					bot.Send(msg)

					delete(unbindStep, telegramID)
					delete(cachedUserDevices, telegramID)
					return
				}

				if step, ok := bindStep[telegramID]; ok {
					switch step {
					case "awaiting_public_key":
						publicKey := strings.TrimSpace(text)
						if len(publicKey) != 44 {
							bot.Send(tgbotapi.NewMessage(chatID, "❌ Public Key должен быть длиной 44 символа. Попробуйте снова."))
							return
						}

						var existing models.Device
						err := db.Where("public_key = ?", publicKey).First(&existing).Error
						if err == nil {
							bot.Send(tgbotapi.NewMessage(chatID, "⚠️ Устройство с таким ключом уже привязано."))
							delete(bindStep, telegramID)
							return
						} else if !errors.Is(err, gorm.ErrRecordNotFound) {
							bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при проверке ключа. Попробуйте позже."))
							delete(bindStep, telegramID)
							return
						}

						pendingBindKey[telegramID] = publicKey
						bindStep[telegramID] = "awaiting_device_name"
						bot.Send(tgbotapi.NewMessage(chatID, "✍️ Введите название устройства."))
						return

					case "awaiting_device_name":
						deviceName := strings.TrimSpace(text)
						if deviceName == "" {
							bot.Send(tgbotapi.NewMessage(chatID, "⚠️ Название устройства не может быть пустым. Попробуйте снова."))
							return
						}

						publicKey, ok := pendingBindKey[telegramID]
						if !ok {
							bot.Send(tgbotapi.NewMessage(chatID, "⚠️ Public Key не найден. Начните процесс заново."))
							delete(bindStep, telegramID)
							return
						}

						var user models.User
						if err := db.First(&user, "telegram_id = ?", telegramID).Error; err != nil {
							bot.Send(tgbotapi.NewMessage(chatID, "Вы ещё не зарегистрированы. Напишите /start"))
							delete(bindStep, telegramID)
							delete(pendingBindKey, telegramID)
							return
						}

						device := models.Device{
							UserID:     user.ID,
							PublicKey:  publicKey,
							DeviceName: deviceName,
							CreatedAt:  time.Now(),
						}

						if err := db.Create(&device).Error; err != nil {
							bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при сохранении устройства. Попробуйте позже."))
						} else {
							msg := tgbotapi.NewMessage(chatID,
								fmt.Sprintf("✅ Устройство *%s* успешно привязано!\n🔐 Public Key: `%s`", deviceName, publicKey))
							msg.ParseMode = "Markdown"
							msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
							bot.Send(msg)
						}

						delete(bindStep, telegramID)
						delete(pendingBindKey, telegramID)
						return
					}
				}
				if update.Message != nil && strings.HasPrefix(update.Message.Text, "/setsub") {
					handleSetSubCommand(bot, update, db, adminTelegramID)
					return
				}
				// Обработка состояния добавления подписки
				if step, ok := addSubStep[telegramID]; ok && step == "awaiting_user_selection" {
					num, err := strconv.Atoi(text)
					if err != nil || num < 1 {
						msg := tgbotapi.NewMessage(chatID, "Введите корректный номер клиента.")
						msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
						bot.Send(msg)
						return
					}

					users, ok := cachedUsers[adminTelegramID]
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
						handleClientsCommand(bot, chatID, telegramID, db)
					}

				case text == "/status":
					handleStatusCommand(bot, chatID, db, telegramID, adminTelegramID)

				case strings.HasPrefix(text, "/bind"):
					handleBindCommand(bot, chatID, db, telegramID, text)

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
				case "unbind":
					var user models.User
					if err := db.First(&user, "telegram_id = ?", telegramID).Error; err != nil {
						msg := tgbotapi.NewMessage(chatID, "Вы ещё не зарегистрированы. Напишите /start")
						bot.Send(msg)
						return
					}

					var devices []models.Device
					db.Where("user_id = ?", user.ID).Find(&devices)

					if len(devices) == 0 {
						msg := tgbotapi.NewMessage(chatID, "⚠️ У вас нет привязанных устройств.")
						bot.Send(msg)
						return
					}

					cachedUserDevices[telegramID] = devices
					unbindStep[telegramID] = true

					text := "📱 Ваши устройства:\n"
					for i, d := range devices {
						text += fmt.Sprintf("%d) %s — `%s`\n", i+1, d.DeviceName, d.PublicKey)
					}
					text += "\nВведите номер устройства для отвязки."

					msg := tgbotapi.NewMessage(chatID, text)
					msg.ParseMode = "Markdown"
					bot.Send(msg)

				case "sub_add_30", "sub_add_90", "sub_add_180", "sub_add_360":
					handleSubscriptionAdd(bot, db, telegramID, chatID, data)
				case "sub_add_delete":
					handleSubscriptionDelete(bot, db, telegramID, chatID)
				case "status":
					handleStatusCommand(bot, chatID, db, telegramID, adminTelegramID)
				case "clients":
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ У вас нет прав для просмотра клиентов.")
						msg.ReplyMarkup = getMainMenuKeyboard(false)
						bot.Send(msg)
					} else {
						handleClientsCommand(bot, chatID, telegramID, db)
					}
				case "bind":
					bindStep[telegramID] = "awaiting_public_key"
					msg := tgbotapi.NewMessage(chatID, "🔐 Введите ваш Public Key (44 символа).")
					bot.Send(msg)
				case "addsub":
					if telegramID != adminTelegramID {
						msg := tgbotapi.NewMessage(chatID, "❌ Только администратор может добавлять подписки.")
						msg.ReplyMarkup = getMainMenuKeyboard(true)
						bot.Send(msg)
					} else {
						var users []models.User
						db.Find(&users)
						cachedUsers[adminTelegramID] = users
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

	users, ok := cachedUsers[adminTelegramID]
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

	users, ok := cachedUsers[adminTelegramID]
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

func handleClientsCommand(bot *tgbotapi.BotAPI, chatID int64, adminTelegramID int64, db *gorm.DB) {
	var users []models.User
	err := db.Find(&users).Error
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при получении пользователей из базы."))
		return
	}

	cachedUsers[adminTelegramID] = users // Обновляем кэш

	if len(users) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "Пользователи не найдены."))
		return
	}

	text := fmt.Sprintf("👥 Зарегистрированные пользователи (%d):\n\n", len(users))
	for i, user := range users {
		username := user.Username
		if username == "" {
			username = "(без username)"
		}

		// Получаем активную подписку
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

	if unlimitedUsers[telegramID] {
		statusText += "✅ Статус: Активен (Неограниченная подписка)\n"
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
	var rows [][]tgbotapi.InlineKeyboardButton

	if isAdmin {
		rows = [][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Статус", "status"),
				tgbotapi.NewInlineKeyboardButtonData("Клиенты", "clients"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Привязать устройство", "bind"),
				tgbotapi.NewInlineKeyboardButtonData("Отвязать устройство", "unbind"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Добавить подписку", "addsub"),
			),
		}
	} else {
		rows = [][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Статус", "status"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Привязать устройство", "bind"),
				tgbotapi.NewInlineKeyboardButtonData("Отвязать устройство", "unbind"),
			),
		}
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
			if unlimitedUsers[sub.User.TelegramID] {
				continue
			}
			if sub.User.TelegramID == 0 {
				log.Printf("Подписка %d не имеет связанного пользователя", sub.ID)
				continue
			}

			var text string
			switch daysLeft {
			case 1:
				text = "⏳ У вас заканчивается подписка через 1 день. Для продления напишите в личные сообщения @GooogeR"
			case 3:
				text = "⏳ У вас заканчивается подписка через 3 дня. Для продления напишите в личные сообщения @GooogeR"
			case 7:
				text = "⏳ У вас заканчивается подписка через 7 дней. Для продления напишите в личные сообщения @GooogeR"
			default:
				text = fmt.Sprintf("⏳ У вас заканчивается подписка через %d дней. Для продления напишите в личные сообщения @GooogeR", daysLeft)
			}
			msg := tgbotapi.NewMessage(sub.User.TelegramID, text)
			if _, err := bot.Send(msg); err != nil {
				log.Printf("Ошибка отправки напоминания пользователю %d: %v", sub.User.TelegramID, err)
			}
		}
	}
}
func parseUnlimitedUsers(env string) map[int64]bool {
	result := make(map[int64]bool)
	for _, idStr := range strings.Split(env, ",") {
		idStr = strings.TrimSpace(idStr)
		if idStr == "" {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil {
			result[id] = true
		}
	}
	return result
}

func handleSetSubCommand(bot *tgbotapi.BotAPI, update tgbotapi.Update, db *gorm.DB, adminTelegramID int64) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	if update.Message == nil {
		log.Println("handleSetSubCommand: update.Message is nil")
		return
	}

	text := update.Message.Text
	log.Printf("handleSetSubCommand called by telegramID=%d with text=%s", telegramID, text)

	if telegramID != adminTelegramID {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Команда доступна только администратору"))
		return
	}

	parts := strings.Fields(text)
	if len(parts) != 3 {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный формат. Используйте: /setsub <telegram_id> <дд-мм-гггг>"))
		return
	}

	targetTelegramID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || targetTelegramID <= 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный Telegram ID пользователя"))
		return
	}

	date, err := time.Parse("02-01-2006", parts[2])
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Неверный формат даты. Используйте дд-мм-гггг"))
		return
	}

	if date.Before(time.Now()) {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Дата не может быть в прошлом"))
		return
	}

	// Ищем пользователя по Telegram ID
	var user models.User
	err = db.Where("telegram_id = ?", targetTelegramID).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Пользователь не найден"))
		} else {
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка базы данных"))
			log.Printf("Ошибка при поиске пользователя: %v", err)
		}
		return
	}

	now := time.Now()

	// Ищем активную подписку
	var sub models.Subscription
	err = db.Where("user_id = ? AND expires_at > ?", user.ID, now).
		Order("expires_at DESC").
		First(&sub).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Создаём новую подписку
		newSub := models.Subscription{
			UserID:    user.ID,
			Title:     "Подписка",
			ExpiresAt: date,
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
		// Обновляем дату подписки
		sub.ExpiresAt = date
		if err := db.Save(&sub).Error; err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при обновлении подписки."))
			return
		}
	}

	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf(
		"✅ Подписка пользователя @%s успешно установлена до %s",
		user.Username, date.Format("02-01-2006"))))
}
