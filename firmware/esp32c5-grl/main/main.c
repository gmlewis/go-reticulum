/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#include <stdio.h>
#include <string.h>
#include "esp_log.h"
#include "esp_system.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include "esp32c5_entropy.h"
#include "esp32c5_wifi.h"
#include "esp32c5_uart.h"
#include "esp32c5_i2c.h"

static const char *TAG = "grl_main";

void app_main(void) {
    ESP_LOGI(TAG, "=======================================================");
    ESP_LOGI(TAG, "  Go Reticulum Lifesaver (GRL) - ESP32-C5 Appliance   ");
    ESP_LOGI(TAG, "=======================================================");

    // 1. Initialize and verify hardware entropy from SAR ADC / RC_FAST
    ESP_LOGI(TAG, "[1/4] Enabling SAR ADC thermal noise generator for TRNG...");
    grl_entropy_enable();

    uint8_t mac[6];
    grl_read_efuse_mac(mac);
    ESP_LOGI(TAG, "      Device eFuse MAC: %02X:%02X:%02X:%02X:%02X:%02X",
             mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);

    uint32_t test_word = grl_entropy_read_word();
    ESP_LOGI(TAG, "      Sample hardware random word: 0x%08X", (unsigned int)test_word);

    // 2. Bring up Wi-Fi 6 SoftAP and Captive Portal DNS interception
    char ssid[32];
    snprintf(ssid, sizeof(ssid), "Reticulum-Lifesaver-%02X%02X", mac[4], mac[5]);
    ESP_LOGI(TAG, "[2/4] Starting Wi-Fi 6 SoftAP ('%s')...", ssid);
    grl_wifi_init_softap(ssid);

    // 3. Initialize GNSS receiver UART (GPIO 9 RX, GPIO 10 TX @ 9600 baud)
    ESP_LOGI(TAG, "[3/4] Initializing GNSS UART1 on GPIO 9/10 (9600 baud)...");
    grl_gnss_uart_init(GRL_GNSS_DEFAULT_BAUD);

    // 4. Initialize Digital Compass I2C bus (GPIO 21 SDA, GPIO 22 SCL @ 100 kHz)
    ESP_LOGI(TAG, "[4/4] Initializing QMC5883L I2C bus on GPIO 21/22...");
    grl_i2c_init();

    ESP_LOGI(TAG, "All hardware subsystems initialized successfully.");
    ESP_LOGI(TAG, "Connect a smartphone to Wi-Fi SSID '%s' to view the survival dashboard.", ssid);

    // Main status heartbeat loop
    while (1) {
        vTaskDelay(pdMS_TO_TICKS(10000));
        ESP_LOGI(TAG, "GRL nominal | Free heap: %u bytes", (unsigned int)esp_get_free_heap_size());
    }
}
