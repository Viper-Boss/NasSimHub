package modemmanager

import "testing"

func TestEpdgFQDN(t *testing.T) {
	cases := []struct {
		plmn string
		want string
	}{
		{"46011", "epdg.epc.mnc011.mcc460.pub.3gppnetwork.org"},
		{"460-11", "epdg.epc.mnc011.mcc460.pub.3gppnetwork.org"},
		{"310260", "epdg.epc.mnc260.mcc310.pub.3gppnetwork.org"},
		{"", ""},
		{"abc", ""},
		{"1234", ""},
		{"1234567", ""},
	}
	for _, testCase := range cases {
		if got := epdgFQDN(testCase.plmn); got != testCase.want {
			t.Fatalf("epdgFQDN(%q) = %q, want %q", testCase.plmn, got, testCase.want)
		}
	}
}

func TestParseQNWINFOPlmn(t *testing.T) {
	cases := []struct {
		response string
		want     string
	}{
		{`+QNWINFO: "FDD LTE","46001","LTE BAND 3",1650`, "46001"},
		{`+QNWINFO: "FDD LTE","46011","LTE BAND 1",300`, "46011"},
		{`+QNWINFO: "NO SERVICE"`, ""},
		{`+QNWINFO: "FDD LTE","","BAND"`, ""},
		{`+QNWINFO: "FDD LTE","CHN-CT","LTE BAND 3"`, ""},
		{``, ""},
	}
	for _, testCase := range cases {
		if got := parseQNWINFOPlmn(testCase.response); got != testCase.want {
			t.Fatalf("parseQNWINFOPlmn(%q) = %q, want %q", testCase.response, got, testCase.want)
		}
	}
}
