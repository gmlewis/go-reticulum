/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#ifndef ESP32C5_WIFI_H
#define ESP32C5_WIFI_H

#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

// Initialize Wi-Fi 6 SoftAP and start the captive portal DNS interception server.
esp_err_t grb_wifi_init_softap(const char *ssid);

#ifdef __cplusplus
}
#endif

#endif // ESP32C5_WIFI_H
