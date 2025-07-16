package wireguard

import (
	"errors"
	"fmt"
	"os/exec"
)

// AddClientViaDocker создает клиента WireGuard через Docker и возвращает конфиг, QR-код и ошибку
func AddClientViaDocker(deviceName string) (string, []byte, error) {
	// Здесь должен быть код запуска Docker контейнера wg-easy для создания клиента
	// Например (пример условный):
	out, err := exec.Command("docker", "exec", "wg-easy", "add-client", deviceName).Output()
	if err != nil {
		return "", nil, fmt.Errorf("ошибка добавления клиента: %w", err)
	}

	config := string(out)
	qr := generateQRCode(config) // Функция генерации QR-кода (реализуй отдельно)

	return config, qr, nil
}

func generateQRCode(config string) []byte {
	// Логика генерации QR из конфига (можно использовать сторонние библиотеки)
	// Возвращаем PNG в []byte
	return []byte{}
}

// Можно добавить функции удаления клиента, обновления и т.д.
func RemoveClient(deviceName string) error {
	// Пример вызова docker для удаления клиента
	cmd := exec.Command("docker", "exec", "wg-easy", "remove-client", deviceName)
	if err := cmd.Run(); err != nil {
		return errors.New("ошибка удаления клиента: " + err.Error())
	}
	return nil
}
