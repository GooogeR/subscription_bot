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

var (
	addSubStep        = make(map[int64]string)
	selectedUserIndex = make(map[int64]int)
	cachedUsers       = make(map[int64][]models.User)
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
		if update.Message != nil {
			chatID := update.Message.Chat.ID
			userName := update.Message.From.UserName
			telegramID := update.Message.From.ID
			text := update.Message.Text

			// --- Новый блок: обработка пошагового удаления/продления подписки ---
			if step, ok := addSubStep[telegramID]; ok && step == "awaiting_remove_user_selection" {
				num, err := strconv.Atoi(text)
				if err != nil || num < 1 || num > len(cachedUsers[telegramID]) {
					msg := tgbotapi.NewMessage(chatID, "Введите корректный номер клиента.")
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
					continue
				}
				selectedUserIndex[telegramID] = num - 1
				addSubStep[telegramID] = "awaiting_remove_duration"

				keyboard := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить подписку", "remove_sub_delete"),
					),
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("30 дней", "remove_sub_30"),
						tgbotapi.NewInlineKeyboardButtonData("90 дней", "remove_sub_90"),
					),
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("180 дней", "remove_sub_180"),
						tgbotapi.NewInlineKeyboardButtonData("360 дней", "remove_sub_360"),
					),
				)
				msg := tgbotapi.NewMessage(chatID, "Выберите действие:")
				msg.ReplyMarkup = keyboard
				bot.Send(msg)
				continue
			}
			// --- Конец нового блока ---

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

			// Отвечаем на callback, чтобы убрать "часики"
			callbackConfig := tgbotapi.CallbackConfig{
				CallbackQueryID: callback.ID,
				Text:            "",
			}
			_, err := bot.Request(callbackConfig)
			if err != nil {
				log.Printf("Error sending callback answer: %v", err)
			}
			// Обработка нажатий на кнопки
			switch data {
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
					// Собираем список клиентов
					var users []models.User
					db.Find(&users)
					textList := "Клиенты:\n"
					for i, u := range users {
						username := u.Username
						if username == "" {
							username = "(без username)"
						}
						textList += fmt.Sprintf("%d) @%s — ID: %d\n", i+1, username, u.TelegramID)
					}
					textList += "\nИспользуйте команду /admin addsub <telegramID> <дни> или /admin removesub <telegramID>"

					msg := tgbotapi.NewMessage(chatID, textList)
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
				}
			case "removesub":
				if telegramID != adminTelegramID {
					msg := tgbotapi.NewMessage(chatID, "❌ Только администратор может удалять подписки.")
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
				} else {
					var users []models.User
					db.Find(&users)
					cachedUsers[telegramID] = users
					addSubStep[telegramID] = "awaiting_remove_user_selection"

					textList := "Клиенты:\n"
					for i, u := range users {
						username := u.Username
						if username == "" {
							username = "(без username)"
						}
						textList += fmt.Sprintf("%d) @%s — ID: %d\n", i+1, username, u.TelegramID)
					}
					textList += "\nВведите номер клиента для удаления или продления подписки."

					msg := tgbotapi.NewMessage(chatID, textList)
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
				}
			case "remove_sub_delete", "remove_sub_30", "remove_sub_90", "remove_sub_180", "remove_sub_360":
				days := 0
				if data != "remove_sub_delete" {
					days, _ = strconv.Atoi(strings.TrimPrefix(data, "remove_sub_"))
				}

				list, ok := cachedUsers[telegramID]
				if !ok || len(list) == 0 {
					msg := tgbotapi.NewMessage(chatID, "Ошибка: список клиентов не найден.")
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
					break
				}
				index, ok := selectedUserIndex[telegramID]
				if !ok || index < 0 || index >= len(list) {
					msg := tgbotapi.NewMessage(chatID, "Ошибка: выбранный пользователь не найден.")
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
					break
				}
				user := list[index]

				var dbUser models.User
				result := db.First(&dbUser, "telegram_id = ?", user.TelegramID)
				if result.Error != nil {
					msg := tgbotapi.NewMessage(chatID, "Пользователь не найден в базе.")
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
					break
				}

				if data == "remove_sub_delete" {
					if err := db.Where("user_id = ?", dbUser.ID).Delete(&models.Subscription{}).Error; err != nil {
						msg := tgbotapi.NewMessage(chatID, "Ошибка при удалении подписки.")
						msg.ReplyMarkup = getMainMenuKeyboard(true)
						bot.Send(msg)
						break
					}
					msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("🗑️ Подписка пользователя @%s удалена.", dbUser.Username))
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
				} else {
					var sub models.Subscription
					now := time.Now()
					result := db.Where("user_id = ? AND expires_at > ?", dbUser.ID, now).
						Order("expires_at desc").
						First(&sub)
					if result.Error != nil {
						sub = models.Subscription{
							UserID:    dbUser.ID,
							Title:     "Подписка",
							ExpiresAt: now.AddDate(0, 0, days),
							CreatedAt: now,
						}
						if err := db.Create(&sub).Error; err != nil {
							msg := tgbotapi.NewMessage(chatID, "Ошибка при создании подписки.")
							msg.ReplyMarkup = getMainMenuKeyboard(true)
							bot.Send(msg)
							break
						}
					} else {
						sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, days)
						if err := db.Save(&sub).Error; err != nil {
							msg := tgbotapi.NewMessage(chatID, "Ошибка при обновлении подписки.")
							msg.ReplyMarkup = getMainMenuKeyboard(true)
							bot.Send(msg)
							break
						}
					}
					msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Подписка пользователя @%s обновлена на %d дней. Новая дата: %s",
						dbUser.Username, days, sub.ExpiresAt.Format("02-01-2006")))
					msg.ReplyMarkup = getMainMenuKeyboard(true)
					bot.Send(msg)
				}

				delete(addSubStep, telegramID)
				delete(selectedUserIndex, telegramID)
				delete(cachedUsers, telegramID)
			default:
				msg := tgbotapi.NewMessage(chatID, "Неизвестная команда с кнопки.")
				msg.ReplyMarkup = getMainMenuKeyboard(telegramID == adminTelegramID)
				bot.Send(msg)
			}
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
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Добавить подписку", "addsub"),
			tgbotapi.NewInlineKeyboardButtonData("Убрать подписку", "removesub"),
		))
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}
