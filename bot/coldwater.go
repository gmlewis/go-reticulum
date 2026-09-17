// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the cold-water immersion protocol: the one-minute, ten-minute,
// one-hour sequence, and the survival windows that follow from the water
// temperature.
//
// Everything here is offline, because the moment it is needed there is no
// network: a person is in the water, and somebody on shore is reading a screen.
// The physics it encodes is the reason cold water kills so much faster than
// people expect. The gasp reflex fires within seconds and drowns people who can
// swim perfectly well; muscle control in the hands and arms goes in about ten
// minutes, which is why swimming to a shore that is a hundred meters away is
// usually not an option; and hypothermia, the thing people actually worry
// about, is the third problem rather than the first. Without a lifejacket,
// drowning beats hypothermia to it every time.
//
// The water temperature is the input that matters, and it is worth carrying a
// thermometer for: the air can be warm enough to swim in while the water is
// cold enough to disable a swimmer in ten minutes.

package bot

import (
	"fmt"
	"strconv"
	"strings"
)

// Cold-water wordings.
const (
	// coldwaterUsage is the usage line for the command.
	coldwaterUsage = "coldwater [temp_f|temp_c]"
	// coldwaterRuleHeader is the header of the survival rule.
	coldwaterRuleHeader = "[COLD WATER 1-10-1 RULE]"
)

// coldWaterBand is one temperature band of the immersion table.
type coldWaterBand struct {
	// MaxF is the band's upper bound in Fahrenheit, exclusive.
	MaxF float64
	// Risk is the headline warning.
	Risk string
	// SwimFailure is how long useful swimming lasts.
	SwimFailure string
	// Survival is how long a person with a flotation device survives.
	Survival string
	// Action is what to do about it.
	Action string
}

// coldWaterBands is the immersion table, coldest first. The bounds are the
// published ones, in Fahrenheit; the Celsius equivalents in the comments are
// the round numbers the table is taught with.
var coldWaterBands = []coldWaterBand{
	{
		MaxF:        32.5, // 0°C
		Risk:        "DANGER: Immediate Swim Failure!",
		SwimFailure: "< 15 min",
		Survival:    "15-45 min",
		Action: "Action: you have minutes, not hours. Get out now, and do not try to swim any distance — " +
			"float and call for help.",
	},
	{
		MaxF:        40, // 4°C
		Risk:        "DANGER: High Risk of Swim Failure!",
		SwimFailure: "15-30 min",
		Survival:    "30-90 min",
		Action: "Action: get out immediately. Keep your head up and your airway clear; the gasp reflex " +
			"is what drowns people in the first minute.",
	},
	{
		MaxF:        50, // 10°C
		Risk:        "DANGER: High Risk of Swim Failure!",
		SwimFailure: "30-60 min",
		Survival:    "1-3 hours",
		Action:      "Action: HUDDLE position or HELP posture (arms tight, knees to chest) to retain core heat.",
	},
	{
		MaxF:        60, // 15°C
		Risk:        "CAUTION: Rapid Cooling and Swim Failure Risk",
		SwimFailure: "1-2 hours",
		Survival:    "1-6 hours",
		Action:      "Action: HUDDLE or HELP posture, and get out before your hands stop working.",
	},
	{
		MaxF:        70, // 21°C
		Risk:        "CAUTION: Cold Shock and Gradual Cooling",
		SwimFailure: "2-7 hours",
		Survival:    "2-40 hours",
		Action:      "Action: keep moving toward an exit; cold shock and swim failure are still possible.",
	},
	{
		MaxF:        coldWaterWarmF,
		Risk:        "LOW RISK: Cold Shock Still Possible",
		SwimFailure: "many hours",
		Survival:    "indefinite with a flotation device",
		Action: "Action: cold shock can still strike below about 77°F; clear your airway, float first, " +
			"then move.",
	},
}

// coldWaterWarmF is the top of the table, which is the temperature above which
// cold-water survival limits stop applying to a clothed adult.
const coldWaterWarmF = 101

// coldwaterBandFor returns the band a water temperature falls in.
func coldwaterBandFor(celsius float64) coldWaterBand {
	fahrenheit := celsius*9/5 + 32
	for _, band := range coldWaterBands {
		if fahrenheit < band.MaxF {
			return band
		}
	}
	return coldWaterBands[len(coldWaterBands)-1]
}

// runColdwater answers with the survival rule, or with the window that applies
// at a given water temperature.
func (c *commandContext) runColdwater() []string {
	args := strings.TrimSpace(c.Args)
	if args == "" {
		return []string{
			coldwaterRuleHeader + " 1 minute: cold shock — panic, hyperventilation, gasp reflex. " +
				"Keep your airway clear and control your breathing; DO NOT swim immediately.",
			"10 minutes: cold incapacitation — loss of muscle control in fingers and arms; swimming " +
				"failure. Get out or secure your lifejacket NOW.",
			"1 hour: hypothermia — loss of consciousness. Without a lifejacket, drowning comes before " +
				"hypothermia does.",
		}
	}
	celsius, ok := parseColdWaterTemperature(args)
	if !ok {
		return []string{"Usage: " + coldwaterUsage,
			"a temperature is a number with a unit, like 48F or 8.9C; a bare number is Fahrenheit"}
	}
	band := coldwaterBandFor(celsius)
	return []string{
		fmt.Sprintf("[COLD WATER %.0f°F / %.1f°C] %v", celsius*9/5+32, celsius, band.Risk),
		fmt.Sprintf("Cold Shock: 1 min gasp reflex | Swim Failure: %v | Survival: %v",
			band.SwimFailure, band.Survival),
		band.Action,
	}
}

// parseColdWaterTemperature parses a water temperature with its unit. A bare
// number is read as Fahrenheit, which the answer states in both scales so a
// wrong assumption is visible rather than hidden.
func parseColdWaterTemperature(text string) (float64, bool) {
	trimmed := strings.TrimSpace(strings.ToUpper(text))
	unit := byte(0)
	if len(trimmed) > 0 {
		last := trimmed[len(trimmed)-1]
		if last == 'F' || last == 'C' {
			unit = last
			trimmed = strings.TrimSpace(trimmed[:len(trimmed)-1])
		}
	}
	trimmed = strings.TrimSuffix(trimmed, "°")
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "DEG"))
	value, err := strconv.ParseFloat(strings.TrimSpace(trimmed), 64)
	if err != nil {
		return 0, false
	}
	switch unit {
	case 'C':
		return value, true
	case 'F':
		return (value - 32) * 5 / 9, true
	default:
		// No unit: Fahrenheit, which is what the colder coastal waters are
		// usually quoted in.
		return (value - 32) * 5 / 9, true
	}
}
