/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#ifndef ESP32C5_UART_H
#define ESP32C5_UART_H

#include "esp_err.h"
#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

#define GRL_GNSS_UART_NUM      UART_NUM_1
#define GRL_GNSS_PIN_RX        9
#define GRL_GNSS_PIN_TX        10
#define GRL_GNSS_DEFAULT_BAUD  9600

// Initialize UART1 for the NMEA-0183 GNSS receiver.
esp_err_t grl_gnss_uart_init(int baud_rate);

// Read incoming GNSS NMEA bytes from the UART ring buffer.
int grl_gnss_uart_read(uint8_t *buf, size_t length, uint32_t timeout_ms);

#ifdef __cplusplus
}
#endif

#endif // ESP32C5_UART_H
