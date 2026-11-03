// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

const Base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const letters = "abcdefghijklmnopqrstuvwxyz"

var validFilenamePattern = regexp.MustCompile(`^[^\\/:*?"<>|]{1,255}$`)

// BcryptPasswordHasher provides the production password hashing behavior.
type BcryptPasswordHasher struct{}

func (BcryptPasswordHasher) Hash(password string) (string, error) {
	return HashPassword(password)
}

func (BcryptPasswordHasher) Verify(password, encodedHash string) bool {
	return CheckPasswordHash(password, encodedHash)
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func SHA256(input string, count int) string {
	sum := []byte(input)

	if count <= 0 {
		return input
	}

	for i := 0; i < count; i++ {
		hash := sha256.Sum256(sum)
		sum = hash[:]
	}

	return hex.EncodeToString(sum)
}

func RemoveSpaces(input string) string {
	return strings.ReplaceAll(input, " ", "")
}

func GenerateRandomUUID() string {
	return uuid.New().String()
}

func GenerateDeterministicUUID(input string) string {
	hasher := sha256.New()
	hasher.Write([]byte(input))
	hash := hasher.Sum(nil)
	return uuid.NewSHA1(uuid.NameSpaceURL, hash).String()
}

func GenerateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}
	return string(result)
}

func PartialStringInSlice(a string, list []string) bool {
	for _, b := range list {
		if strings.Contains(a, b) {
			return true
		}
	}
	return false
}

func StringToUint64(s string) uint64 {
	r, error := strconv.ParseUint(s, 10, 64)

	if error != nil {
		return 0
	}

	return r
}

func ParseJWT(tokenString string) (any, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT token format")
	}

	token, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("error parsing token: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("error extracting claims")
	}

	customClaims := make(map[string]interface{})
	for k, v := range claims {
		if k != "exp" && k != "jti" {
			customClaims[k] = v
		}
	}

	return customClaims, nil
}

func BytesToSize(toType string, bytes float64) float64 {
	switch toType {
	case "KB":
		return bytes / 1024
	case "MB":
		return bytes / 1024 / 1024
	case "GB":
		return bytes / 1024 / 1024 / 1024
	case "TB":
		return bytes / 1024 / 1024 / 1024 / 1024
	default:
		return bytes
	}
}

func HumanFormatToSize(size string) uint64 {
	size = strings.TrimSpace(size)
	re := regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*([kmgtp]?b?)$`)

	matches := re.FindStringSubmatch(size)

	if len(matches) != 3 {
		reScientific := regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?(?:e[+-]?\d+)?)\s*([kmgtp]?b?)$`)
		matches = reScientific.FindStringSubmatch(size)
		if len(matches) != 3 {
			return 0
		}
	}

	num, err := strconv.ParseFloat(matches[1], 64)
	if err != nil || num < 0 {
		return 0
	}

	unit := strings.ToUpper(matches[2])
	if unit == "" {
		unit = "B"
	} else if !strings.HasSuffix(unit, "B") {
		unit += "B"
	}

	var multiplier float64
	switch unit {
	case "B":
		multiplier = 1
	case "KB":
		multiplier = 1 << 10
	case "MB":
		multiplier = 1 << 20
	case "GB":
		multiplier = 1 << 30
	case "TB":
		multiplier = 1 << 40
	case "PB":
		multiplier = 1 << 50
	default:
		return 0
	}

	maxVal := float64(^uint64(0))
	result := num * multiplier

	if num > maxVal/multiplier {
		return ^uint64(0)
	}

	if result >= maxVal {
		return ^uint64(0)
	}

	return uint64(result)
}

func EncodeBase62(num uint64, length int) string {
	res := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		res[i] = Base62Chars[num%62]
		num /= 62
	}
	return string(res)
}

func ShortHash(input string) string {
	hash := sha256.Sum256([]byte(input))
	num := binary.BigEndian.Uint64(hash[:8]) >> 16
	return EncodeBase62(num, 8)
}

func IsValidVMName(name string) bool {
	regex := regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
	return regex.MatchString(name)
}

func IsValidHostname(name string) bool {
	v := validator.New()
	err := v.Var(name, "hostname_rfc1123")
	return err == nil
}

func IsValidMACAddress(mac string) bool {
	regex := regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})$`)
	return regex.MatchString(mac)
}

func GenerateRandomMAC() string {
	mac := make([]byte, 6)
	_, err := rand.Read(mac)
	if err != nil {
		return ""
	}

	mac[0] &= 0xFE
	mac[0] |= 0x02

	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}

func IsValidEmail(email string) bool {
	if email == "" {
		return false
	}

	validator := validator.New()
	err := validator.Var(email, "email")

	if err != nil {
		return false
	}

	return true
}

func IsValidUsername(username string) bool {
	invalidUsernames := []string{"root", "admin", "superuser"}
	for _, invalid := range invalidUsernames {
		if strings.EqualFold(username, invalid) {
			return false
		}
	}

	regex := regexp.MustCompile(`^[a-z_]([a-z0-9_-]{0,31}|[a-z0-9_-]{0,30}\$)$`)
	return regex.MatchString(username)
}

func IsValidWorkgroup(name string) bool {
	if len(name) == 0 || len(name) > 15 {
		return false
	}

	validPattern := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !validPattern.MatchString(name) {
		return false
	}

	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") {
		return false
	}

	return true
}

func IsValidServerString(s string) bool {
	return utf8.ValidString(s) && len(s) <= 100
}

func RemoveDuplicates(input []string) []string {
	seen := make(map[string]struct{})
	var result []string

	for _, val := range input {
		val = strings.TrimSpace(val)
		if _, ok := seen[val]; !ok && val != "" {
			seen[val] = struct{}{}
			result = append(result, val)
		}
	}

	return result
}

func IsValidGroupName(name string) bool {
	if len(name) == 0 || len(name) > 32 {
		return false
	}

	validPattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !validPattern.MatchString(name) {
		return false
	}

	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") {
		return false
	}

	return true
}

var validCountryCodes = map[string]struct{}{
	"AF": {}, "AL": {}, "DZ": {}, "AS": {}, "AD": {}, "AO": {}, "AI": {}, "AQ": {}, "AG": {}, "AR": {},
	"AM": {}, "AW": {}, "AP": {}, "AU": {}, "AT": {}, "AZ": {}, "BS": {}, "BH": {}, "BD": {}, "BB": {},
	"BY": {}, "BE": {}, "BZ": {}, "BJ": {}, "BM": {}, "BT": {}, "BO": {}, "BQ": {}, "BA": {}, "BW": {},
	"BV": {}, "BR": {}, "IO": {}, "BN": {}, "BG": {}, "BF": {}, "BI": {}, "KH": {}, "CM": {}, "CA": {},
	"CV": {}, "KY": {}, "CF": {}, "TD": {}, "CL": {}, "CN": {}, "CX": {}, "CC": {}, "CO": {}, "KM": {},
	"CG": {}, "CD": {}, "CK": {}, "CR": {}, "HR": {}, "CU": {}, "CW": {}, "CY": {}, "CZ": {}, "CI": {},
	"DK": {}, "DJ": {}, "DM": {}, "DO": {}, "EC": {}, "EG": {}, "SV": {}, "GQ": {}, "ER": {}, "EE": {},
	"ET": {}, "FK": {}, "FO": {}, "FJ": {}, "FI": {}, "FR": {}, "GF": {}, "PF": {}, "TF": {}, "GA": {},
	"GM": {}, "GE": {}, "DE": {}, "GH": {}, "GI": {}, "GR": {}, "GL": {}, "GD": {}, "GP": {}, "GU": {},
	"GT": {}, "GG": {}, "GN": {}, "GW": {}, "GY": {}, "HT": {}, "HM": {}, "VA": {}, "HN": {}, "HK": {},
	"HU": {}, "IS": {}, "IN": {}, "ID": {}, "IR": {}, "IQ": {}, "IE": {}, "IM": {}, "IL": {}, "IT": {},
	"JM": {}, "JP": {}, "JE": {}, "JO": {}, "KZ": {}, "KE": {}, "KI": {}, "KR": {}, "KW": {}, "KG": {},
	"LA": {}, "LV": {}, "LB": {}, "LS": {}, "LR": {}, "LY": {}, "LI": {}, "LT": {}, "LU": {}, "MO": {},
	"MG": {}, "MW": {}, "MY": {}, "MV": {}, "ML": {}, "MT": {}, "MH": {}, "MQ": {}, "MR": {}, "MU": {},
	"YT": {}, "MX": {}, "FM": {}, "MD": {}, "MC": {}, "MN": {}, "ME": {}, "MS": {}, "MA": {}, "MZ": {},
	"MM": {}, "NA": {}, "NR": {}, "NP": {}, "NL": {}, "AN": {}, "NC": {}, "NZ": {}, "NI": {}, "NE": {},
	"NG": {}, "NU": {}, "NF": {}, "KP": {}, "MK": {}, "MP": {}, "NO": {}, "OM": {}, "PK": {}, "PW": {},
	"PS": {}, "PA": {}, "PG": {}, "PY": {}, "PE": {}, "PH": {}, "PN": {}, "PL": {}, "PT": {}, "PR": {},
	"QA": {}, "RE": {}, "RO": {}, "RU": {}, "RW": {}, "BL": {}, "SH": {}, "KN": {}, "LC": {}, "MF": {},
	"PM": {}, "VC": {}, "WS": {}, "SM": {}, "ST": {}, "SA": {}, "SN": {}, "RS": {}, "CS": {}, "SC": {},
	"SL": {}, "SG": {}, "SX": {}, "SK": {}, "SI": {}, "SB": {}, "SO": {}, "ZA": {}, "GS": {}, "SS": {},
	"ES": {}, "LK": {}, "SD": {}, "SR": {}, "SJ": {}, "SZ": {}, "SE": {}, "CH": {}, "SY": {}, "TW": {},
	"TJ": {}, "TZ": {}, "TH": {}, "TL": {}, "TG": {}, "TK": {}, "TO": {}, "TT": {}, "TN": {}, "TR": {},
	"TM": {}, "TC": {}, "TV": {}, "UG": {}, "UA": {}, "AE": {}, "GB": {}, "US": {}, "UM": {}, "UY": {},
	"UZ": {}, "VU": {}, "VE": {}, "VN": {}, "VG": {}, "VI": {}, "WF": {}, "EH": {}, "YE": {}, "ZM": {},
	"ZW": {}, "AX": {},
}

func IsValidCountryCode(code string) bool {
	if len(code) != 2 {
		return false
	}

	code = strings.ToUpper(code)
	_, ok := validCountryCodes[code]

	return ok
}

func IsValidFilename(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("file name cannot be blank")
	}
	if name == "." || name == ".." {
		return errors.New("invalid file name")
	}
	if !utf8.ValidString(name) || len([]byte(name)) > 255 {
		return errors.New("invalid file name")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("invalid file name")
		}
	}

	if !validFilenamePattern.MatchString(name) {
		return errors.New("invalid file name")
	}
	return nil
}

func MakeValidHostname(name string) string {
	name = strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9-]`)
	name = re.ReplaceAllString(name, "-")
	name = regexp.MustCompile(`-+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")

	if name == "" {
		name = "host"
	}

	if len(name) > 63 {
		name = name[:63]
	}

	return name
}

func HashIntToNLetters(n int, length int) string {
	if n < 0 {
		n = 0
	}

	// Compute 26^length
	max := new(big.Int).Exp(big.NewInt(26), big.NewInt(int64(length)), nil).Int64()
	if int64(n) >= max {
		panic("HashIntToNLetters: n too large, would cause collisions")
	}

	num := n
	out := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		out[i] = letters[num%26]
		num /= 26
	}

	return string(out) // unique & deterministic as long as n < 26^length
}

func PreviousMAC(macStr string) (string, error) {
	hw, err := net.ParseMAC(macStr)
	if err != nil {
		return "", fmt.Errorf("invalid MAC address: %w", err)
	}

	hw = hw[:6]

	for i := len(hw) - 1; i >= 0; i-- {
		if hw[i] > 0 {
			hw[i]--
			break
		}
		hw[i] = 0xFF
	}

	return hw.String(), nil
}

func SplitIPv4AndMask(cidr string) (string, string, error) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", fmt.Errorf("invalid CIDR: %w", err)
	}

	mask := network.Mask
	ipStr := ip.String()
	maskStr := net.IP(mask).String()

	return ipStr, maskStr, nil
}

func IsMagnetURI(uri string) bool {
	if !strings.HasPrefix(uri, "magnet:?") {
		return false
	}

	u, err := url.Parse(uri)
	if err != nil {
		return false
	}

	q := u.Query()

	btihRegex := regexp.MustCompile(`^urn:btih:[a-zA-Z0-9]{32,40}$`)
	if !btihRegex.MatchString(q.Get("xt")) {
		return false
	}

	if q.Get("dn") == "" {
		return false
	}

	if q.Get("tr") == "" {
		return false
	}

	return true
}

func FormatMAC(mac []byte) string {
	return net.HardwareAddr(mac).String()
}

func PtrIfNonZero(v uint) *uint {
	if v == 0 {
		return nil
	}
	return &v
}

func IntOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func RemoveStringFromSlice(slice []string, str string) []string {
	result := []string{}
	for _, s := range slice {
		if s != str {
			result = append(result, s)
		}
	}
	return result
}

func MergeMaps(maps ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}

func IsValidYAML(data string) bool {
	var out interface{}
	err := yaml.Unmarshal([]byte(data), &out)
	return err == nil
}

func IsValidZFSPoolName(name string) bool {
	reserved := []string{"log", "mirror", "raidz", "raidz1", "raidz2", "raidz3", "spare"}

	if name == "" {
		return false
	}

	for _, r := range reserved {
		if strings.HasPrefix(name, r) {
			return false
		}
	}

	if !regexp.MustCompile(`^[a-zA-Z]`).MatchString(name) {
		return false
	}

	if !regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`).MatchString(name) {
		return false
	}

	if strings.Contains(name, "%") {
		return false
	}

	if regexp.MustCompile(`^c[0-9]`).MatchString(name) {
		return false
	}

	return true
}

func SplitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// ErrorCode returns the lowercased text before the first ':' in err, e.g. "vm_not_found".
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.ToLower(strings.TrimSpace(err.Error()))
	if idx := strings.IndexByte(code, ':'); idx >= 0 {
		code = code[:idx]
	}
	return strings.TrimSpace(code)
}

// ErrorCodes returns every ':'-separated part of err as a set of lowercased codes.
func ErrorCodes(err error) map[string]struct{} {
	codes := make(map[string]struct{})
	if err == nil {
		return codes
	}

	for _, part := range strings.Split(strings.ToLower(err.Error()), ":") {
		if code := strings.TrimSpace(part); code != "" {
			codes[code] = struct{}{}
		}
	}
	return codes
}

// HasErrorCode reports whether codes contains any of candidates.
func HasErrorCode(codes map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := codes[candidate]; ok {
			return true
		}
	}
	return false
}

// UniqueIDs returns ids with zeros and duplicates removed, keeping first-seen order.
func UniqueIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
