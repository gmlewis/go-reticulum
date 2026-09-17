// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the tactical unit conversions: the arithmetic a person does
// in the field with cold hands and a headlamp, when the answer decides whether
// a stove has fuel, whether a battery can carry the radio through the night, or
// what an altimeter setting means in the units the chart uses.
//
// Every conversion is a factor to a single base unit per dimension, so a new
// unit is one table entry and a dimension cannot silently mix with another.
// Temperature is the one exception, because its scales have offsets rather than
// just factors, and battery capacity is its own case because it is a product of
// charge and voltage rather than a simple conversion.
//
// The water units are deliberately included: a gallon of water weighs
// 8.345 pounds and a liter weighs a kilogram, which is exactly the arithmetic a
// hiker does when deciding whether to carry the extra bottle.

package bot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// convUsage is the usage line for the command.
const convUsage = "conv <value><unit> <target>"

// conversionDimensions are the dimensions a unit may belong to. Two units can
// only be converted when they share one, which is what stops a distance being
// reported as a pressure.
const (
	dimensionLength      = "length"
	dimensionSpeed       = "speed"
	dimensionPressure    = "pressure"
	dimensionMass        = "mass"
	dimensionEnergy      = "energy"
	dimensionTemperature = "temperature"
	dimensionCharge      = "charge"
)

// unitDef is one unit: the dimension it belongs to and how many base units of
// that dimension one of it is worth.
type unitDef struct {
	dimension string
	factor    float64
	// display is the canonical spelling the answer prints.
	display string
	// decimals is how many decimal places the answer prints for this unit.
	decimals int
}

// conversionUnits is every unit the command understands, keyed by the
// lowercase spelling a person types.
var conversionUnits = map[string]unitDef{
	// Length, base meter.
	"m":     {dimensionLength, 1, "m", 2},
	"meter": {dimensionLength, 1, "m", 2},
	"km":    {dimensionLength, 1000, "km", 2},
	"cm":    {dimensionLength, 0.01, "cm", 1},
	"mm":    {dimensionLength, 0.001, "mm", 1},
	"mi":    {dimensionLength, 1609.344, "mi", 2},
	"mile":  {dimensionLength, 1609.344, "mi", 2},
	"nm":    {dimensionLength, 1852, "nm", 2},
	"nmi":   {dimensionLength, 1852, "nm", 2},
	"ft":    {dimensionLength, 0.3048, "ft", 1},
	"feet":  {dimensionLength, 0.3048, "ft", 1},
	"yd":    {dimensionLength, 0.9144, "yd", 1},
	"in":    {dimensionLength, 0.0254, "in", 2},

	// Speed, base meter per second.
	"mps":   {dimensionSpeed, 1, "m/s", 2},
	"m/s":   {dimensionSpeed, 1, "m/s", 2},
	"kmh":   {dimensionSpeed, 1000.0 / 3600.0, "km/h", 1},
	"kph":   {dimensionSpeed, 1000.0 / 3600.0, "km/h", 1},
	"km/h":  {dimensionSpeed, 1000.0 / 3600.0, "km/h", 1},
	"mph":   {dimensionSpeed, 0.44704, "mph", 1},
	"kt":    {dimensionSpeed, 1852.0 / 3600.0, "kt", 1},
	"kts":   {dimensionSpeed, 1852.0 / 3600.0, "kt", 1},
	"knot":  {dimensionSpeed, 1852.0 / 3600.0, "kt", 1},
	"knots": {dimensionSpeed, 1852.0 / 3600.0, "kt", 1},
	"fps":   {dimensionSpeed, 0.3048, "ft/s", 1},

	// Pressure, base pascal.
	"pa":   {dimensionPressure, 1, "Pa", 0},
	"kpa":  {dimensionPressure, 1000, "kPa", 2},
	"hpa":  {dimensionPressure, 100, "hPa", 2},
	"mbar": {dimensionPressure, 100, "mbar", 2},
	"mb":   {dimensionPressure, 100, "mbar", 2},
	"bar":  {dimensionPressure, 100000, "bar", 3},
	"inhg": {dimensionPressure, 3386.389, "inHg", 2},
	"mmhg": {dimensionPressure, 133.322387415, "mmHg", 2},
	"psi":  {dimensionPressure, 6894.757293168, "psi", 2},
	"atm":  {dimensionPressure, 101325, "atm", 4},

	// Mass, base kilogram. The water spellings are here rather than in their
	// own dimension because a volume of water is a mass, which is the whole
	// point of the conversion.
	"kg":           {dimensionMass, 1, "kg", 2},
	"g":            {dimensionMass, 0.001, "g", 0},
	"lb":           {dimensionMass, 0.45359237, "lbs", 2},
	"lbs":          {dimensionMass, 0.45359237, "lbs", 2},
	"pound":        {dimensionMass, 0.45359237, "lbs", 2},
	"oz":           {dimensionMass, 0.028349523125, "oz", 2},
	"l_water":      {dimensionMass, 1, "L water", 2},
	"liter_water":  {dimensionMass, 1, "L water", 2},
	"liters_water": {dimensionMass, 1, "L water", 2},
	"litre_water":  {dimensionMass, 1, "L water", 2},
	"gal_water":    {dimensionMass, 3.785411784, "gal water", 2},
	"gals_water":   {dimensionMass, 3.785411784, "gal water", 2},

	// Energy, base watt-hour, which is the unit battery capacity is sold in.
	"wh":  {dimensionEnergy, 1, "Wh", 2},
	"kwh": {dimensionEnergy, 1000, "kWh", 3},
	"j":   {dimensionEnergy, 1.0 / 3600.0, "J", 0},
	"kj":  {dimensionEnergy, 1000.0 / 3600.0, "kJ", 2},

	// Charge, base milliamp-hour.
	"mah": {dimensionCharge, 1, "mAh", 0},
	"ah":  {dimensionCharge, 1000, "Ah", 2},
}

// milliampHoursPerAmpHour is the bridge between the charge dimension's base
// unit and the amp-hours a voltage multiplies.
const milliampHoursPerAmpHour = 1000.0

// ConvertUnits converts a value from one unit to another. Units must belong to
// the same dimension, and temperature is handled by its own scale conversion.
func ConvertUnits(value float64, from, to string) (float64, error) {
	source, ok := conversionUnits[normalizeUnit(from)]
	if !ok {
		if _, temp := temperatureScale(from); temp {
			return convertTemperature(value, from, to)
		}
		return 0, fmt.Errorf("conv: unknown unit %q", strings.TrimSpace(from))
	}
	target, ok := conversionUnits[normalizeUnit(to)]
	if !ok {
		if _, temp := temperatureScale(to); temp {
			// A temperature to a non-temperature is not a conversion.
			return 0, fmt.Errorf("conv: %v cannot be converted to %v", displayUnit(from), displayUnit(to))
		}
		return 0, fmt.Errorf("conv: unknown unit %q", strings.TrimSpace(to))
	}
	if source.dimension != target.dimension {
		return 0, fmt.Errorf("conv: %v cannot be converted to %v (%v to %v)",
			displayUnit(from), displayUnit(to), source.dimension, target.dimension)
	}
	return value * source.factor / target.factor, nil
}

// temperatureScale reports whether a unit names a temperature scale, and which
// one.
func temperatureScale(unit string) (string, bool) {
	switch normalizeUnit(unit) {
	case "c", "celsius", "centigrade":
		return "celsius", true
	case "f", "fahrenheit":
		return "fahrenheit", true
	case "k", "kelvin":
		return "kelvin", true
	default:
		return "", false
	}
}

// convertTemperature converts between the three temperature scales, whose
// offsets mean a plain factor would be wrong.
func convertTemperature(value float64, from, to string) (float64, error) {
	fromScale, ok := temperatureScale(from)
	if !ok {
		return 0, fmt.Errorf("conv: %v cannot be converted to %v", displayUnit(from), displayUnit(to))
	}
	toScale, ok := temperatureScale(to)
	if !ok {
		return 0, fmt.Errorf("conv: %v cannot be converted to %v", displayUnit(from), displayUnit(to))
	}
	celsius := value
	switch fromScale {
	case "fahrenheit":
		celsius = (value - 32) * 5 / 9
	case "kelvin":
		celsius = value - 273.15
	}
	switch toScale {
	case "fahrenheit":
		return celsius*9/5 + 32, nil
	case "kelvin":
		return celsius + 273.15, nil
	default:
		return celsius, nil
	}
}

// normalizeUnit lowercases a unit spelling and strips the punctuation that
// separates the symbols from the words.
func normalizeUnit(unit string) string {
	trimmed := strings.ToLower(strings.TrimSpace(unit))
	trimmed = strings.TrimPrefix(trimmed, "degrees")
	trimmed = strings.TrimPrefix(trimmed, "degree")
	trimmed = strings.TrimSuffix(trimmed, ".")
	return strings.TrimSpace(trimmed)
}

// displayUnit renders a unit the way the answer should print it, even when the
// unit is unknown. The temperature scales are spelled with their degree sign,
// because that is how a chart and a thermometer spell them.
func displayUnit(unit string) string {
	if def, ok := conversionUnits[normalizeUnit(unit)]; ok {
		return def.display
	}
	if scale, ok := temperatureScale(unit); ok {
		switch scale {
		case "celsius":
			return "°C"
		case "fahrenheit":
			return "°F"
		default:
			return "K"
		}
	}
	return strings.TrimSpace(unit)
}

// formatMeasurement renders a converted value with the precision its unit
// deserves.
func formatMeasurement(value float64, unit string) string {
	decimals := 2
	switch {
	case conversionUnits[normalizeUnit(unit)].display != "":
		decimals = conversionUnits[normalizeUnit(unit)].decimals
	default:
		if _, ok := temperatureScale(unit); ok {
			// A tenth of a degree is as fine as any thermometer an operator
			// carries.
			decimals = 1
		}
	}
	return fmt.Sprintf("%.*f %v", decimals, value, displayUnit(unit))
}

// runConv performs one conversion.
func (c *commandContext) runConv() []string {
	fields := strings.Fields(strings.TrimSpace(c.Args))
	if len(fields) == 0 {
		return []string{"Usage: " + convUsage, conversionHelpLine()}
	}
	// The source may be written as one word ("5000mAh@3.7V") or as a value and
	// a unit separated by a space ("29.92 inHg"), so both shapes are accepted.
	var source, target string
	switch len(fields) {
	case 2:
		source, target = fields[0], fields[1]
	case 3:
		source, target = fields[0]+fields[1], fields[2]
	default:
		return []string{"Usage: " + convUsage, conversionHelpLine()}
	}

	if battery, ok := parseBatterySource(source); ok {
		value, err := convertBattery(battery, target)
		if err != nil {
			return []string{err.Error()}
		}
		return []string{formatMeasurement(value, target)}
	}

	value, unit, ok := splitValueUnit(source)
	if !ok {
		return []string{"Usage: " + convUsage, conversionHelpLine()}
	}
	converted, err := ConvertUnits(value, unit, target)
	if err != nil {
		// A charge and an energy are related only through a voltage, so the
		// useful answer names the voltage rather than the dimension mismatch.
		if missingVoltage(unit, target) {
			return []string{"conv: crossing between battery charge and energy needs a voltage, like 5000mAh@3.7V Wh"}
		}
		return []string{err.Error()}
	}
	return []string{formatMeasurement(converted, target)}
}

// splitValueUnit splits "29.92inHg" into its value and its unit.
func splitValueUnit(source string) (float64, string, bool) {
	split := 0
	for split < len(source) && (source[split] == '.' || source[split] == '-' || source[split] == '+' ||
		source[split] == 'e' || source[split] == 'E' ||
		(source[split] >= '0' && source[split] <= '9')) {
		split++
	}
	if split == 0 || split == len(source) {
		return 0, "", false
	}
	value, err := strconv.ParseFloat(source[:split], 64)
	if err != nil {
		return 0, "", false
	}
	return value, source[split:], true
}

// batterySource is a parsed battery figure: a charge or an energy, optionally
// with the voltage that ties the two together.
type batterySource struct {
	value      float64
	unit       string
	voltage    float64
	hasVoltage bool
}

// parseBatterySource recognizes a battery figure: a charge or an energy with
// the voltage that ties the two together, written "5000mAh@3.7V", "2.5Ah@12V",
// or "50Wh@12V". A source with no voltage is an ordinary unit conversion, so it
// reports false and is handled by the plain table.
func parseBatterySource(source string) (batterySource, bool) {
	value, rest, found := strings.Cut(strings.TrimSpace(source), "@")
	if !found {
		return batterySource{}, false
	}
	number, unit, ok := splitValueUnit(value)
	if !ok {
		return batterySource{}, false
	}
	voltage, ok := parseVoltage(rest)
	if !ok {
		return batterySource{}, false
	}
	switch normalizeUnit(unit) {
	case "mah", "ah", "wh", "kwh", "j", "kj":
	default:
		return batterySource{}, false
	}
	return batterySource{value: number, unit: unit, voltage: voltage, hasVoltage: true}, true
}

// parseVoltage parses the "@3.7V" tail of a battery figure.
func parseVoltage(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "V"), "v")
	trimmed = strings.TrimSpace(trimmed)
	voltage, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || voltage <= 0 {
		return 0, false
	}
	return voltage, true
}

// convertBattery converts between charge and energy at a stated voltage.
func convertBattery(source batterySource, target string) (float64, error) {
	targetUnit := normalizeUnit(target)
	sourceUnit := normalizeUnit(source.unit)

	sourceCharge := sourceUnit == "mah" || sourceUnit == "ah"
	sourceEnergy := !sourceCharge
	targetCharge := targetUnit == "mah" || targetUnit == "ah"
	targetEnergy := !targetCharge

	switch {
	case sourceCharge && targetCharge, sourceEnergy && targetEnergy:
		// A figure converted within its own dimension needs no voltage, and the
		// one it carries is ignored.
		sourceFactor := conversionUnits[sourceUnit].factor
		targetFactor := conversionUnits[targetUnit].factor
		if targetFactor == 0 {
			return 0, fmt.Errorf("conv: unknown unit %q", displayUnit(target))
		}
		return source.value * sourceFactor / targetFactor, nil
	case sourceCharge && targetEnergy:
		if !source.hasVoltage {
			return 0, errors.New("conv: a battery charge needs a voltage, like 5000mAh@3.7V")
		}
		if _, ok := conversionUnits[targetUnit]; !ok || conversionUnits[targetUnit].dimension != dimensionEnergy {
			return 0, fmt.Errorf("conv: %v cannot be converted to %v", displayUnit(source.unit), displayUnit(target))
		}
		// Charge to watt-hours: amp-hours times volts. The charge dimension's
		// base unit is the milliamp-hour, so the amp-hours that the voltage
		// multiplies are a thousandth of it.
		ampHours := source.value * conversionUnits[sourceUnit].factor / milliampHoursPerAmpHour
		return ampHours * source.voltage / conversionUnits[targetUnit].factor, nil
	case sourceEnergy && targetCharge:
		if !source.hasVoltage {
			return 0, errors.New("conv: a battery energy needs a voltage to give a charge, like 50Wh@12V")
		}
		if _, ok := conversionUnits[targetUnit]; !ok || conversionUnits[targetUnit].dimension != dimensionCharge {
			return 0, fmt.Errorf("conv: %v cannot be converted to %v", displayUnit(source.unit), displayUnit(target))
		}
		wattHours := source.value * conversionUnits[sourceUnit].factor
		return wattHours / source.voltage * milliampHoursPerAmpHour / conversionUnits[targetUnit].factor, nil
	default:
		return 0, fmt.Errorf("conv: %v cannot be converted to %v", displayUnit(source.unit), displayUnit(target))
	}
}

// missingVoltage reports whether a conversion failed because it crossed between
// battery charge and battery energy, which only a voltage can bridge.
func missingVoltage(from, to string) bool {
	fromUnit, fromOK := conversionUnits[normalizeUnit(from)]
	toUnit, toOK := conversionUnits[normalizeUnit(to)]
	if !fromOK || !toOK {
		return false
	}
	return (fromUnit.dimension == dimensionCharge && toUnit.dimension == dimensionEnergy) ||
		(fromUnit.dimension == dimensionEnergy && toUnit.dimension == dimensionCharge)
}

// conversionHelpLine names the dimensions and their units, which is the only
// way a person learns what the command accepts without guessing.
func conversionHelpLine() string {
	dims := []struct {
		name  string
		units string
	}{
		{"pressure/altimeter", "inHg, hPa, mbar, mmHg, psi, atm"},
		{"length", "km, mi, nm, m, ft, yd, cm, mm, in"},
		{"speed", "kt, mph, km/h, m/s, ft/s"},
		{"temperature", "C, F, K"},
		{"mass and water", "kg, lbs, oz, l_water, gal_water"},
		{"energy and battery", "Wh, kWh, J, mAh@V, Ah@V"},
	}
	parts := make([]string, 0, len(dims))
	for _, dim := range dims {
		parts = append(parts, dim.name+" ("+dim.units+")")
	}
	return "Units: " + strings.Join(parts, "; ")
}
