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
