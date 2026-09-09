package modemmanager

import "testing"

func TestParseServingCell(t *testing.T) {
	response := `+QENG: "servingcell","LIMSRV","LTE","FDD",460,01,C2FCC1F,207,1650,3,5,5,E624,-102,-11,-71,4,25`
	cell := parseServingCell(response)
	if cell == nil {
		t.Fatal("parseServingCell returned nil")
	}
	if cell.State != "LIMSRV" || cell.RAT != "LTE" || cell.Duplex != "FDD" {
		t.Fatalf("unexpected identity fields: %+v", cell)
	}
	if cell.PLMN != "46001" {
		t.Fatalf("PLMN = %q, want 46001", cell.PLMN)
	}
	if cell.CellIDHex != "C2FCC1F" {
		t.Fatalf("CellIDHex = %q", cell.CellIDHex)
	}
	if cell.PCI != 207 {
		t.Fatalf("PCI = %d, want 207", cell.PCI)
	}
	if cell.EARFCN != 1650 {
		t.Fatalf("EARFCN = %d, want 1650", cell.EARFCN)
	}
	if cell.Band != "B3" {
		t.Fatalf("Band = %q, want B3", cell.Band)
	}
	if cell.UplinkBandwidthMHz != 20 || cell.DownlinkBandwidthMHz != 20 {
		t.Fatalf("bandwidths = %v/%v, want 20/20", cell.UplinkBandwidthMHz, cell.DownlinkBandwidthMHz)
	}
	if cell.TACHex != "E624" {
		t.Fatalf("TACHex = %q", cell.TACHex)
	}
	if cell.RSRP != -102 || cell.RSRQ != -11 || cell.RSSI != -71 || cell.SINR != 4 {
		t.Fatalf("signal fields wrong: %+v", cell)
	}
	// EARFCN 1650 on B3: 1805 + 0.1*(1650-1200) = 1850 MHz.
	if cell.FrequencyMHz != 1850 {
		t.Fatalf("FrequencyMHz = %v, want 1850", cell.FrequencyMHz)
	}
}

func TestParseServingCellRejectsGarbage(t *testing.T) {
	for _, response := range []string{"", "+QENG: \"servingcell\"", "ERROR", "+COPS: 2"} {
		if cell := parseServingCell(response); cell != nil {
			t.Fatalf("parseServingCell(%q) = %+v, want nil", response, cell)
		}
	}
}

func TestLteDownlinkFrequencyMHz(t *testing.T) {
	cases := []struct {
		earfcn uint32
		want   float64
	}{
		{1650, 1850},
		{1850, 1870},
		{100, 2120},
		{0, 2110},
	}
	for _, testCase := range cases {
		if got := lteDownlinkFrequencyMHz(testCase.earfcn); got != testCase.want {
			t.Fatalf("lteDownlinkFrequencyMHz(%d) = %v, want %v", testCase.earfcn, got, testCase.want)
		}
	}
}

func TestNormaliseBand(t *testing.T) {
	cases := map[string]string{
		"3":   "B3",
		"78":  "B78",
		"n78": "n78",
		"B1":  "B1",
		"":    "",
	}
	for input, want := range cases {
		if got := normaliseBand(input); got != want {
			t.Fatalf("normaliseBand(%q) = %q, want %q", input, got, want)
		}
	}
}
