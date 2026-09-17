// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"math"
	"strings"
	"testing"
)

// TestConvertUnitsMatchesGoldens asserts each conversion matches the factor the
// unit is defined by, across every dimension the command supports.
func TestConvertUnitsMatchesGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
		from  string
		to    string
		want  float64
	}{
		{"inHg to hPa", 29.92, "inHg", "hPa", 29.92 * 3386.389 / 100},
		{"hPa to inHg", 1013.25, "hPa", "inHg", 1013.25 * 100 / 3386.389},
		{"psi to mbar", 14.7, "psi", "mbar", 14.7 * 6894.757293168 / 100},
		{"mmHg to hPa", 760, "mmHg", "hPa", 760 * 133.322387415 / 100},
		{"miles to kilometers", 1, "mi", "km", 1.609344},
		{"kilometers to miles", 100, "km", "mi", 100 * 1000 / 1609.344},
		{"nautical miles to meters", 1, "nm", "m", 1852},
		{"feet to meters", 1000, "ft", "m", 304.8},
		{"yards to feet", 100, "yd", "ft", 300},
		{"knots to miles per hour", 100, "kt", "mph", 100 * (1852.0 / 3600.0) / 0.44704},
		{"kilometers per hour to meters per second", 36, "kmh", "mps", 10},
		{"gallons of water to pounds", 5, "gal_water", "lbs", 5 * 3.785411784 / 0.45359237},
		{"liters of water to kilograms", 2, "l_water", "kg", 2},
		{"pounds to kilograms", 10, "lbs", "kg", 10 * 0.45359237},
		{"kilowatt hours to joules", 1, "kWh", "J", 3.6e6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ConvertUnits(tc.value, tc.from, tc.to)
			if err != nil {
				t.Fatalf("ConvertUnits(%v, %v, %v): %v", tc.value, tc.from, tc.to, err)
			}
			if math.Abs(got-tc.want) > math.Abs(tc.want)*1e-12 {
				t.Errorf("ConvertUnits(%v, %v, %v) = %v, want %v", tc.value, tc.from, tc.to, got, tc.want)
			}
		})
	}
}

// TestConvertTemperatureScales asserts the three temperature scales convert
// with their offsets, which a plain factor would get wrong.
func TestConvertTemperatureScales(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value float64
		from  string
		to    string
		want  float64
	}{
		{0, "C", "F", 32},
		{100, "C", "F", 212},
		{-40, "C", "F", -40},
		{32, "F", "C", 0},
		{212, "F", "C", 100},
		{0, "C", "K", 273.15},
		{273.15, "K", "C", 0},
		{32, "F", "K", 273.15},
	}
	for _, tc := range tests {
		got, err := ConvertUnits(tc.value, tc.from, tc.to)
		if err != nil {
			t.Errorf("ConvertUnits(%v, %v, %v): %v", tc.value, tc.from, tc.to, err)
			continue
		}
		if !closeWithin(got, tc.want, 1e-9) {
			t.Errorf("ConvertUnits(%v, %v, %v) = %v, want %v", tc.value, tc.from, tc.to, got, tc.want)
		}
	}
}

// TestConvertUnitsRejectsImpossibleConversions asserts a unit from another
// dimension, or a unit that does not exist, is refused rather than answered
// with a nonsensical number.
func TestConvertUnitsRejectsImpossibleConversions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ from, to string }{
		{"km", "kg"}, {"hPa", "kt"}, {"C", "km"}, {"km", "C"},
		{"furlongs", "km"}, {"km", "furlongs"},
	} {
		if got, err := ConvertUnits(1, tc.from, tc.to); err == nil {
			t.Errorf("ConvertUnits(1, %v, %v) = %v, want an error", tc.from, tc.to, got)
		}
	}
}

// TestConvCommandFormatsAnswers asserts the command prints the documented
// shapes, including the battery figure whose value is a product of charge and
// voltage.
func TestConvCommandFormatsAnswers(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	tests := []struct {
		line string
		want string
	}{
		{"conv 29.92inHg hPa", "1013.21 hPa"},
		{"conv 29.92 inHg hPa", "1013.21 hPa"},
		{"conv 5000mAh@3.7V Wh", "18.50 Wh"},
		{"conv 5000mAh@3.7V Ah", "5.00 Ah"},
		{"conv 5000mAh Ah", "5.00 Ah"},
		{"conv 50Wh@12V Wh", "50.00 Wh"},
		{"conv 50Wh@12V mAh", "4167 mAh"},
		{"conv 1mi km", "1.61 km"},
		{"conv 5gal_water lbs", "41.73 lbs"},
		{"conv 2l_water kg", "2.00 kg"},
		{"conv 0C F", "32.0 °F"},
		{"conv 100kt mph", "115.1 mph"},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			lines := runLines(t, reg, session, tc.line)
			if len(lines) != 1 || lines[0] != tc.want {
				t.Errorf("%q = %v, want %q", tc.line, lines, tc.want)
			}
		})
	}
}

// TestConvCommandExplainsItself asserts an empty or malformed request lists the
// units, which is the only way a field user learns what is available.
func TestConvCommandExplainsItself(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{"conv", "conv 29.92", "conv 1 2 3 4", "conv nonsense hPa"} {
		lines := runLines(t, reg, session, line)
		if len(lines) == 0 {
			t.Fatalf("%q returned nothing", line)
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "Usage: "+convUsage) && !strings.Contains(joined, "conv:") {
			t.Errorf("%q = %v, want the usage or the reason", line, lines)
		}
	}
	// A battery figure that needs a voltage says so rather than failing
	// obscurely.
	lines := runLines(t, reg, session, "conv 5000mAh Wh")
	if len(lines) != 1 || !strings.Contains(lines[0], "needs a voltage") {
		t.Errorf("conv without a voltage = %v, want the voltage line", lines)
	}
}

// TestConversionHelpLineNamesEveryDimension asserts the help line mentions each
// dimension, so the command's documentation cannot fall behind its table.
func TestConversionHelpLineNamesEveryDimension(t *testing.T) {
	t.Parallel()

	help := conversionHelpLine()
	for _, dimension := range []string{"pressure", "length", "speed", "temperature", "mass", "battery"} {
		if !strings.Contains(help, dimension) {
			t.Errorf("help line %q does not mention %v", help, dimension)
		}
	}
	if !strings.Contains(help, "inHg") || !strings.Contains(help, "gal_water") || !strings.Contains(help, "mAh@V") {
		t.Errorf("help line %q omits an example unit", help)
	}
}
