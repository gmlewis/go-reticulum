/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#include "esp32c5_i2c.h"
#include "driver/i2c.h"
#include "esp_log.h"
#include <string.h>

static const char *TAG = "grl_i2c";
#define I2C_PORT I2C_NUM_0

esp_err_t grl_i2c_init(void) {
    i2c_config_t conf = {
        .mode = I2C_MODE_MASTER,
        .sda_io_num = GRL_I2C_PIN_SDA,
        .scl_io_num = GRL_I2C_PIN_SCL,
        .sda_pullup_en = GPIO_PULLUP_ENABLE,
        .scl_pullup_en = GPIO_PULLUP_ENABLE,
        .master.clk_speed = GRL_I2C_FREQ_HZ,
    };

    ESP_ERROR_CHECK(i2c_param_config(I2C_PORT, &conf));
    ESP_ERROR_CHECK(i2c_driver_install(I2C_PORT, conf.mode, 0, 0, 0));

    ESP_LOGI(TAG, "I2C master initialized on SDA=GPIO %d, SCL=GPIO %d @ %d Hz",
             GRL_I2C_PIN_SDA, GRL_I2C_PIN_SCL, GRL_I2C_FREQ_HZ);
    return ESP_OK;
}

esp_err_t grl_i2c_read_reg(uint8_t addr, uint8_t reg, uint8_t *buf, size_t len) {
    return i2c_master_write_read_device(I2C_PORT, addr, &reg, 1, buf, len, pdMS_TO_TICKS(100));
}

esp_err_t grl_i2c_write_reg(uint8_t addr, uint8_t reg, const uint8_t *buf, size_t len) {
    uint8_t write_buf[len + 1];
    write_buf[0] = reg;
    if (len > 0 && buf != NULL) {
        memcpy(&write_buf[1], buf, len);
    }
    return i2c_master_write_to_device(I2C_PORT, addr, write_buf, len + 1, pdMS_TO_TICKS(100));
}
