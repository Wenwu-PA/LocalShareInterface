package files

import (
	"errors"
	"golang.org/x/text/unicode/norm"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrAccess = errors.New("нет доступа к этому пути")
var ErrConflict = errors.New("объект с таким именем уже существует")
var ErrQuota = errors.New("превышена квота или недостаточно свободного места")

// Names are portable between Windows and Linux. The private service directory is inaccessible.
func Clean(raw string) (string, error) {
	if raw == "" || raw == "." {
		return ".", nil
	}
	if !utf8.ValidString(raw) || len(raw) > 32760 || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "\\") {
		return "", ErrAccess
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	parts := strings.Split(raw, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return "", ErrAccess
		}
		part = norm.NFC.String(part)
		if len(part) > 255 || strings.ContainsAny(part, `<>:"|?*`) {
			return "", errors.New("имя содержит запрещённые символы или слишком длинное")
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return "", ErrAccess
			}
		}
		upper := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		reserved := upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || upper == "CLOCK$" || upper == "CONIN$" || upper == "CONOUT$"
		if strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT") {
			suffix := strings.TrimPrefix(strings.TrimPrefix(upper, "COM"), "LPT")
			reserved = reserved || strings.Contains("123456789¹²³", suffix) && len([]rune(suffix)) == 1
		}
		if reserved || strings.EqualFold(part, ".lanbridge") {
			return "", ErrAccess
		}
		parts[i] = part
	}
	return path.Join(parts...), nil
}
func descendant(name, dir string) bool {
	return name == dir || dir == "." || strings.HasPrefix(name, dir+"/")
}
