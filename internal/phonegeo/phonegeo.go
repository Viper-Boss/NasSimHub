// Package phonegeo resolves Chinese mobile number prefixes to offline
// region and carrier records using the classic phone.dat format
// (see https://github.com/xluohome/phonedata for the format definition).
// The dataset is embedded so the gateway works fully offline.
package phonegeo

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
)

//go:embed phone.dat
var dataFile []byte

const (
	headerLength     = 4
	indexEntryLength = 9
)

// Record is the offline lookup result for one number prefix.
type Record struct {
	Province string
	City     string
	ZipCode  string
	AreaZone string
	Carrier  string
}

var carrierNames = map[byte]string{
	1: "中国移动",
	2: "中国联通",
	3: "中国电信",
	4: "中国电信虚拟运营商",
	5: "中国联通虚拟运营商",
	6: "中国移动虚拟运营商",
	7: "中国广电",
	8: "中国广电虚拟运营商",
}

var (
	dataVersion       = string(dataFile[:headerLength])
	firstRecordOffset = int32(binary.LittleEndian.Uint32(dataFile[headerLength : headerLength*2]))
	errInvalidNumber  = errors.New("not a mainland China mobile number")
	errPrefixNotFound = errors.New("no offline record for this number prefix")
)

// Version reports the dataset version string stored in the file header.
func Version() string {
	return strings.TrimRight(dataVersion, "\x00 ")
}

func lookupPrefix(prefix uint32) (Record, bool) {
	total := int32(len(dataFile))
	left := int32(0)
	right := (total - firstRecordOffset) / indexEntryLength
	for left <= right {
		mid := (left + right) / 2
		offset := firstRecordOffset + mid*indexEntryLength
		if offset < 0 || offset+indexEntryLength > total {
			break
		}
		current := binary.LittleEndian.Uint32(dataFile[offset : offset+4])
		recordOffset := int32(binary.LittleEndian.Uint32(dataFile[offset+4 : offset+8]))
		cardType := dataFile[offset+8]
		switch {
		case current > prefix:
			right = mid - 1
		case current < prefix:
			left = mid + 1
		default:
			if recordOffset < 0 || recordOffset >= total {
				return Record{}, false
			}
			tail := dataFile[recordOffset:]
			end := bytes.IndexByte(tail, 0)
			if end < 0 {
				return Record{}, false
			}
			parts := bytes.Split(tail[:end], []byte("|"))
			if len(parts) < 4 {
				return Record{}, false
			}
			carrier, ok := carrierNames[cardType]
			if !ok {
				carrier = "未知运营商"
			}
			return Record{
				Province: string(parts[0]),
				City:     string(parts[1]),
				ZipCode:  string(parts[2]),
				AreaZone: string(parts[3]),
				Carrier:  carrier,
			}, true
		}
	}
	return Record{}, false
}

// Lookup resolves a mainland China mobile number (11 digits, with or
// without the +86 country code) to its offline region record. Returns
// false when the input is not a mainland mobile number or the prefix is
// not covered by the dataset.
func Lookup(number string) (Record, bool) {
	digits := sanitizeNumber(number)
	if len(digits) != 11 || digits[0] != '1' {
		return Record{}, false
	}
	prefix, err := strconv.ParseUint(digits[:7], 10, 32)
	if err != nil {
		return Record{}, false
	}
	return lookupPrefix(uint32(prefix))
}

// CarrierOf returns the carrier name for a mainland mobile number, or an
// empty string when unknown.
func CarrierOf(number string) string {
	record, ok := Lookup(number)
	if !ok {
		return ""
	}
	return record.Carrier
}

// sanitizeNumber normalises a phone number to the 11-digit mainland
// format. Numbers with an explicit country code are only accepted when
// it is +86; other country codes are rejected outright.
func sanitizeNumber(number string) string {
	number = strings.TrimSpace(number)
	hasPlus := strings.HasPrefix(number, "+")
	if hasPlus {
		if !strings.HasPrefix(number, "+86") {
			return ""
		}
		number = strings.TrimPrefix(number, "+86")
	}
	var builder strings.Builder
	builder.Grow(len(number))
	for _, symbol := range number {
		if symbol >= '0' && symbol <= '9' {
			builder.WriteRune(symbol)
		}
	}
	digits := builder.String()
	if hasPlus {
		if len(digits) != 11 || digits[0] != '1' {
			return ""
		}
		return digits
	}
	if len(digits) > 11 {
		if len(digits) == 13 && strings.HasPrefix(digits, "86") {
			digits = digits[2:]
		} else {
			return ""
		}
	}
	if len(digits) != 11 || digits[0] != '1' {
		return ""
	}
	return digits
}

var _ = binary.BigEndian
