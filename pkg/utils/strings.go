// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
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

func FNVHash(s string) uint64 {
	hasher := fnv.New64a()
	hasher.Write([]byte(s))
	return hasher.Sum64()
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

func PasswordQueryHash(input string) string {
	return SHA256(input, 1)
}

func RemoveSpaces(input string) string {
	return strings.ReplaceAll(input, " ", "")
}

func StringToUintId(s string) uint {
	hasher := fnv.New64a()
	hasher.Write([]byte(s))
	return uint(hasher.Sum64())
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

func StringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
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

func StringToFloat64(s string) float64 {
	r, _ := strconv.ParseFloat(s, 64)
	return r
}

func RemoveEmptyLines(s string) string {
	re := regexp.MustCompile(`(?m)^\n`)
	return re.ReplaceAllString(s, "")
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

/*
 * from zfs diff`s escape function:
 *
 * Prints a file name out a character at a time.  If the character is
 * not in the range of what we consider "printable" ASCII, display it
 * as an escaped 3-digit octal value.  ASCII values less than a space
 * are all control characters and we declare the upper end as the
 * DELete character.  This also is the last 7-bit ASCII character.
 * We choose to treat all 8-bit ASCII as not printable for this
 * application.
 */
func UnescapeFilepath(path string) (string, error) {
	buf := make([]byte, 0, len(path))
	llen := len(path)
	for i := 0; i < llen; {
		if path[i] == '\\' {
			if llen < i+4 {
				return "", fmt.Errorf("invalid octal code: too short")
			}
			octalCode := path[(i + 1):(i + 4)]
			val, err := strconv.ParseUint(octalCode, 8, 8)
			if err != nil {
				return "", fmt.Errorf("invalid octal code: %w", err)
			}
			buf = append(buf, byte(val))
			i += 4
		} else {
			buf = append(buf, path[i])
			i++
		}
	}
	return string(buf), nil
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

func IsIndented(line string) bool {
	return len(line) > 0 && unicode.IsSpace(rune(line[0]))
}

func Contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
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

func JoinStrings(slice []string, sep string) string {
	if len(slice) == 0 {
		return ""
	}
	if len(slice) == 1 {
		return slice[0]
	}
	var sb strings.Builder
	sb.WriteString(slice[0])
	for _, s := range slice[1:] {
		sb.WriteString(sep)
		sb.WriteString(s)
	}
	return sb.String()
}

func MapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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

func IsHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range strings.ToLower(s) {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
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

func JoinStringSlices(slices ...[]string) []string {
	if len(slices) == 0 {
		return nil
	}

	result := make([]string, 0)
	for _, slice := range slices {
		result = append(result, slice...)
	}

	return RemoveDuplicates(result)
}

func SliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	m := make(map[string]int)
	for _, v := range a {
		m[v]++
	}
	for _, v := range b {
		if _, ok := m[v]; !ok || m[v] == 0 {
			return false
		}
		m[v]--
	}

	for _, count := range m {
		if count != 0 {
			return false
		}
	}

	return true
}

func IntSliceToStrSlice(slice []int) []string {
	strSlice := make([]string, len(slice))
	for i, v := range slice {
		strSlice[i] = strconv.Itoa(v)
	}
	return strSlice
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
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("file name cannot be blank")
	}

	validate := validator.New()
	validFileName := regexp.MustCompile(`^[^\\/:*?"<>|]{1,255}$`)

	_ = validate.RegisterValidation("filename", func(fl validator.FieldLevel) bool {
		return validFileName.MatchString(fl.Field().String())
	})

	input := struct {
		Name string `validate:"required,filename"`
	}{Name: name}

	if err := validate.Struct(input); err != nil {
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

func UintSliceToJSON(slice []uint) (string, error) {
	bytes, err := json.Marshal(slice)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func FormatMAC(mac []byte) string {
	return net.HardwareAddr(mac).String()
}

func MustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func IsValidDiskName(name string) bool {
	regex := regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
	return regex.MatchString(name)
}

func IsValidDHCPRange(startIP, endIP string) bool {
	start := net.ParseIP(startIP).To4()
	end := net.ParseIP(endIP).To4()
	if start == nil || end == nil {
		return false
	}

	return bytes.Compare(start, end) < 0
}

func IsValidIPRangeWithSubnet(startIP, endIP, subnet string) bool {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return false
	}

	start := net.ParseIP(startIP)
	end := net.ParseIP(endIP)
	if start == nil || end == nil {
		return false
	}

	ipFamily := func(ip net.IP) int {
		if ip == nil {
			return 0
		}
		if ip.To4() != nil {
			return 4
		}
		if ip.To16() != nil {
			return 6
		}
		return 0
	}

	compareIPs := func(a, b net.IP) int {
		if a4, b4 := a.To4(), b.To4(); a4 != nil && b4 != nil {
			return bytes.Compare(a4, b4)
		}
		return bytes.Compare(a.To16(), b.To16())
	}

	lastIP := func(n *net.IPNet) net.IP {
		base := n.IP.To16()
		if base == nil {
			return nil
		}
		mask := n.Mask
		if len(mask) != net.IPv6len { // normalize to 16 bytes
			m16 := make([]byte, net.IPv6len)
			copy(m16[net.IPv6len-len(mask):], mask)
			mask = m16
		}
		out := make(net.IP, net.IPv6len)
		for i := 0; i < net.IPv6len; i++ {
			out[i] = base[i] | ^mask[i]
		}
		return out
	}

	incIP := func(ip net.IP) net.IP {
		if ip == nil {
			return nil
		}
		ip = append(net.IP(nil), ip...)
		for i := len(ip) - 1; i >= 0; i-- {
			ip[i]++
			if ip[i] != 0 {
				break
			}
		}
		return ip
	}

	decIP := func(ip net.IP) net.IP {
		if ip == nil {
			return nil
		}
		ip = append(net.IP(nil), ip...)
		for i := len(ip) - 1; i >= 0; i-- {
			if ip[i] == 0 {
				ip[i] = 0xFF
				continue
			}
			ip[i]--
			break
		}
		return ip
	}

	fam := ipFamily(ipNet.IP)
	if fam == 0 || ipFamily(start) != fam || ipFamily(end) != fam {
		return false
	}

	if !ipNet.Contains(start) || !ipNet.Contains(end) {
		return false
	}

	if compareIPs(start, end) > 0 {
		return false
	}

	if fam == 4 {
		firstUsable := incIP(ipNet.IP.To4())
		lastUsable := decIP(lastIP(ipNet).To4())

		if firstUsable == nil || lastUsable == nil || bytes.Compare(firstUsable, lastUsable) > 0 {
			return false
		}

		s4, e4 := start.To4(), end.To4()
		if bytes.Compare(s4, firstUsable) < 0 || bytes.Compare(s4, lastUsable) > 0 {
			return false
		}
		if bytes.Compare(e4, firstUsable) < 0 || bytes.Compare(e4, lastUsable) > 0 {
			return false
		}
	}

	return true
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

func IntToString(input int) string {
	return strconv.Itoa(input)
}

func KeepUniqueIntSlice(slice []int) []int {
	seen := make(map[int]struct{}, len(slice))
	out := make([]int, 0, len(slice))

	for _, v := range slice {
		if _, exists := seen[v]; !exists {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}

	return out
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

func HashPasswordSHA512(password string) (string, error) {
	output, err := RunCommand("openssl", "passwd", "-6", password)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}
