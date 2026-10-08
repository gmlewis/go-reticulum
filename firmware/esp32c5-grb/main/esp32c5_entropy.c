/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#include "esp32c5_entropy.h"
#include "esp_random.h"
#include "bootloader_random.h"
#include "esp_mac.h"
#include <string.h>

void grb_entropy_enable(void) {
    bootloader_random_enable();
}

uint32_t grb_entropy_read_word(void) {
    return esp_random();
}

void grb_entropy_read_bytes(uint8_t *buf, size_t len) {
    esp_fill_random(buf, len);
}

void grb_entropy_disable(void) {
    bootloader_random_disable();
}

void grb_read_efuse_mac(uint8_t mac[6]) {
    esp_read_mac(mac, ESP_MAC_WIFI_STA);
}
