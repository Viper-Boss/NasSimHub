// Package smsclass implements the conservative SMS category rules from the
// EC20 gateway design: never miss an important message, accept imperfect
// promo detection. Categories decide push routing:
//
//	OTP     -> Bark urgent notification (Feishu masked)
//	urgent  -> Bark urgent + Feishu full text
//	service -> Bark normal + Feishu full text
//	promo   -> Feishu full text only (no Bark)
//	normal  -> Bark normal + Feishu full text
package smsclass

import (
	"regexp"
	"strings"
)

// Category is the conservative SMS classification result.
type Category string

const (
	CategoryOTP     Category = "otp"
	CategoryUrgent  Category = "urgent"
	CategoryService Category = "service"
	CategoryPromo   Category = "promo"
	CategoryNormal  Category = "normal"
)

var otpKeywords = []string{"验证码", "校验码", "动态密码", "动态码", "授权码", "交易码", "口令", "identification code"}
var otpCodes = regexp.MustCompile(`\d{4,8}`)
var urgentKeywords = []string{"欠费", "停机", "即将用尽", "即将耗尽", "余额不足", "异常", "风险", "冻结", "挂失", "封禁", "违规", "警方", "诈骗"}
var serviceKeywords = []string{"流量", "套餐", "余额", "账单", "缴费", "话费", "积分", "到期", "续费", "订单", "快递", "取件", "物流", "签收"}
var promoKeywords = []string{"贷款", "借款", "额度提升", "房产", "保险", "教育", "升学", "优惠券", "限时", "秒杀", "折扣", "返现", "抽奖", "退订回"}
var promoSignatureHints = []string{"营销", "推广"}

// Classify maps a SMS body to its category using conservative keyword rules.
// A body with an OTP keyword plus a numeric code is always OTP, even when it
// also contains promo words (banks legitimately mix them).
func Classify(text string) Category {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return CategoryNormal
	}
	containsAny := func(keywords []string) bool {
		for _, keyword := range keywords {
			if strings.Contains(t, strings.ToLower(keyword)) {
				return true
			}
		}
		return false
	}
	if containsAny(otpKeywords) && otpCodes.MatchString(t) {
		return CategoryOTP
	}
	if containsAny(urgentKeywords) {
		return CategoryUrgent
	}
	if containsAny(serviceKeywords) {
		return CategoryService
	}
	if containsAny(promoKeywords) {
		return CategoryPromo
	}
	for _, hint := range promoSignatureHints {
		if strings.Contains(t, hint) {
			return CategoryPromo
		}
	}
	return CategoryNormal
}

// Urgent reports whether the category requires an immediate Bark push.
func (c Category) Urgent() bool {
	return c == CategoryOTP || c == CategoryUrgent
}

// FeishuVisible reports whether the full text should go to Feishu. OTP
// messages are masked on Feishu by the caller (only the category and sender
// are logged there; the code itself goes to Bark).
func (c Category) FeishuVisible() bool {
	return c != CategoryOTP
}
