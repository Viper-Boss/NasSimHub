package phonegeo

import "testing"

func TestVersionIsNotEmpty(t *testing.T) {
	if Version() == "" {
		t.Fatal("embedded dataset version is empty")
	}
}

func TestLookupKnownNumbers(t *testing.T) {
	cases := []struct {
		name    string
		number  string
		carrier string
	}{
		{name: "classic CMCC service number prefix", number: "13800138000", carrier: "中国移动"},
		{name: "with country code", number: "+8613800138000", carrier: "中国移动"},
		{name: "formatted with spaces", number: "+86 138 0013 8000", carrier: "中国移动"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			record, ok := Lookup(item.number)
			if !ok {
				t.Fatalf("Lookup(%q) not found", item.number)
			}
			if record.Province == "" || record.City == "" {
				t.Fatalf("Lookup(%q) province/city empty: %+v", item.number, record)
			}
			if record.Carrier != item.carrier {
				t.Fatalf("Lookup(%q) carrier = %q, want %q", item.number, record.Carrier, item.carrier)
			}
		})
	}
}

func TestLookupRejectsInvalidInput(t *testing.T) {
	for _, number := range []string{"", "abc", "12345", "10086", "02160328877", "+15551234567"} {
		if _, ok := Lookup(number); ok {
			t.Fatalf("Lookup(%q) unexpectedly matched", number)
		}
	}
}

func TestCarrierOf(t *testing.T) {
	if CarrierOf("13800138000") != "中国移动" {
		t.Fatalf("CarrierOf returned %q", CarrierOf("13800138000"))
	}
	if CarrierOf("10086") != "" {
		t.Fatalf("CarrierOf short code should be empty")
	}
}
