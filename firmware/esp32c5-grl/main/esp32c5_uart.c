/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#include "esp32c5_uart.h"
#include "driver/uart.h"
#include "esp_log.h"

static const char *TAG = "grl_uart";
#define UART_BUF_SIZE 1024

esp_err_t grl_gnss_uart_init(int baud_rate) {
    if (baud_rate <= 0) {
        baud_rate = GRL_GNSS_DEFAULT_BAUD;
    }

    uart_config_t uart_config = {
        .baud_rate = baud_rate,
        .data_bits = UART_DATA_8_BITS,
        .parity    = UART_PARITY_DISABLE,
        .stop_bits = UART_STOP_BITS_1,
        .flow_ctrl = UART_HW_FLOWCTRL_DISABLE,
        .source_clk = UART_SCLK_DEFAULT,
    };

    ESP_ERROR_CHECK(uart_driver_install(GRL_GNSS_UART_NUM, UART_BUF_SIZE * 2, 0, 0, NULL, 0));
    ESP_ERROR_CHECK(uart_param_config(GRL_GNSS_UART_NUM, &uart_config));
    ESP_ERROR_CHECK(uart_set_pin(GRL_GNSS_UART_NUM, GRL_GNSS_PIN_TX, GRL_GNSS_PIN_RX, UART_PIN_NO_CHANGE, UART_PIN_NO_CHANGE));

    ESP_LOGI(TAG, "GNSS UART1 initialized on RX=GPIO %d, TX=GPIO %d @ %d baud",
             GRL_GNSS_PIN_RX, GRL_GNSS_PIN_TX, baud_rate);
    return ESP_OK;
}

int grl_gnss_uart_read(uint8_t *buf, size_t length, uint32_t timeout_ms) {
    return uart_read_bytes(GRL_GNSS_UART_NUM, buf, length, pdMS_TO_TICKS(timeout_ms));
}
