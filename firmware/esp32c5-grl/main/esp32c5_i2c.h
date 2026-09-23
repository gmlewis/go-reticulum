/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#ifndef ESP32C5_I2C_H
#define ESP32C5_I2C_H

#include "esp_err.h"
#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

#define GRL_I2C_PIN_SDA        21
#define GRL_I2C_PIN_SCL        22
#define GRL_I2C_FREQ_HZ        100000

// Initialize I2C master peripheral on GPIO 21 (SDA) and GPIO 22 (SCL).
esp_err_t grl_i2c_init(void);

// Read len bytes starting from register reg on device at I2C addr.
esp_err_t grl_i2c_read_reg(uint8_t addr, uint8_t reg, uint8_t *buf, size_t len);

// Write len bytes to register reg on device at I2C addr.
esp_err_t grl_i2c_write_reg(uint8_t addr, uint8_t reg, const uint8_t *buf, size_t len);

#ifdef __cplusplus
}
#endif

#endif // ESP32C5_I2C_H
