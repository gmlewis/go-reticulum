/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#ifndef ESP32C5_ENTROPY_H
#define ESP32C5_ENTROPY_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Enable the SAR ADC thermal noise generator for the hardware TRNG.
void grl_entropy_enable(void);

// Read 32-bit words from the hardware RNG peripheral.
uint32_t grl_entropy_read_word(void);

// Read a sequence of random bytes into buf.
void grl_entropy_read_bytes(uint8_t *buf, size_t len);

// Disable the SAR ADC entropy source after initial seeding.
void grl_entropy_disable(void);

// Read the factory eFuse MAC address for use as the entropy gate's salt.
void grl_read_efuse_mac(uint8_t mac[6]);

#ifdef __cplusplus
}
#endif

#endif // ESP32C5_ENTROPY_H
