package smsclass

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		text string
		want Category
	}{
		{"【建设银行】您尾号8834的储蓄卡账户验证码 739264，有效期5分钟", CategoryOTP},
		{"您的动态密码是 882133，请勿泄露", CategoryOTP},
		{"您的账户已欠费，请及时充值，否则将停机", CategoryUrgent},
		{"流量即将用尽，剩余不足100MB", CategoryUrgent},
		{"【中国移动】您本月已使用流量18.6GB", CategoryService},
		{"账单已出，请及时缴费", CategoryService},
		{"【某某金融】额度提升至20万，回复1办理", CategoryPromo},
		{"限时秒杀，全场五折，退订回T", CategoryPromo},
		{"家里宽带白天有人吗？晚上过去找你拿", CategoryNormal},
		{"周末愉快~", CategoryNormal},
		{"", CategoryNormal},
	}
	for _, item := range cases {
		if got := Classify(item.text); got != item.want {
			t.Errorf("Classify(%q) = %q, want %q", item.text, got, item.want)
		}
	}
}

func TestUrgentAndFeishuVisible(t *testing.T) {
	if !CategoryOTP.Urgent() || !CategoryUrgent.Urgent() {
		t.Fatal("otp/urgent must be urgent")
	}
	if CategoryPromo.Urgent() {
		t.Fatal("promo must not be urgent")
	}
	if CategoryOTP.FeishuVisible() {
		t.Fatal("otp must be masked on feishu")
	}
	for _, c := range []Category{CategoryUrgent, CategoryService, CategoryPromo, CategoryNormal} {
		if !c.FeishuVisible() {
			t.Fatalf("%q should be visible on feishu", c)
		}
	}
}
