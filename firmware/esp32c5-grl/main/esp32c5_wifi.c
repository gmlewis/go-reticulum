/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#include "esp32c5_wifi.h"
#include "esp_wifi.h"
#include "esp_event.h"
#include "esp_log.h"
#include "esp_netif.h"
#include "nvs_flash.h"
#include "lwip/sockets.h"
#include "lwip/netdb.h"
#include <string.h>

static const char *TAG = "grl_wifi";

#define DNS_PORT 53

static void dns_server_task(void *pvParameters) {
    uint8_t rx_buffer[128];
    uint8_t tx_buffer[128];

    struct sockaddr_in server_addr;
    server_addr.sin_family = AF_INET;
    server_addr.sin_addr.s_addr = htonl(INADDR_ANY);
    server_addr.sin_port = htons(DNS_PORT);

    int sock = socket(AF_INET, SOCK_DGRAM, IPPROTO_IP);
    if (sock < 0) {
        ESP_LOGE(TAG, "Unable to create DNS socket: errno %d", errno);
        vTaskDelete(NULL);
        return;
    }

    if (bind(sock, (struct sockaddr *)&server_addr, sizeof(server_addr)) < 0) {
        ESP_LOGE(TAG, "Unable to bind DNS socket: errno %d", errno);
        close(sock);
        vTaskDelete(NULL);
        return;
    }

    ESP_LOGI(TAG, "Captive portal DNS server listening on UDP port 53");

    while (1) {
        struct sockaddr_in client_addr;
        socklen_t addr_len = sizeof(client_addr);
        int len = recvfrom(sock, rx_buffer, sizeof(rx_buffer), 0, (struct sockaddr *)&client_addr, &addr_len);
        if (len < 12) {
            continue;
        }

        // Construct standard DNS response redirecting all queries to 192.168.4.1
        memcpy(tx_buffer, rx_buffer, len);
        tx_buffer[2] |= 0x80; // QR = 1 (response)
        tx_buffer[3] |= 0x80; // RA = 1
        tx_buffer[7] = 1;    // ANCOUNT = 1

        int idx = len;
        // Answer name: pointer to Question name (offset 12 = 0xc00c)
        tx_buffer[idx++] = 0xc0;
        tx_buffer[idx++] = 0x0c;
        // Type: A (host address)
        tx_buffer[idx++] = 0x00;
        tx_buffer[idx++] = 0x01;
        // Class: IN (Internet)
        tx_buffer[idx++] = 0x00;
        tx_buffer[idx++] = 0x01;
        // TTL: 60 seconds
        tx_buffer[idx++] = 0x00;
        tx_buffer[idx++] = 0x00;
        tx_buffer[idx++] = 0x00;
        tx_buffer[idx++] = 0x3c;
        // RDLENGTH: 4 bytes (IPv4)
        tx_buffer[idx++] = 0x00;
        tx_buffer[idx++] = 0x04;
        // RDATA: 192.168.4.1 (ESP-IDF SoftAP default IP)
        tx_buffer[idx++] = 192;
        tx_buffer[idx++] = 168;
        tx_buffer[idx++] = 4;
        tx_buffer[idx++] = 1;

        sendto(sock, tx_buffer, idx, 0, (struct sockaddr *)&client_addr, addr_len);
    }
}

esp_err_t grl_wifi_init_softap(const char *ssid) {
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    esp_netif_create_default_wifi_ap();

    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&cfg));

    wifi_config_t wifi_config = {
        .ap = {
            .channel = 1,
            .max_connection = 4,
            .authmode = WIFI_AUTH_OPEN,
            .pmf_cfg = {
                .required = false,
            },
        },
    };

    if (ssid != NULL && strlen(ssid) > 0) {
        strlcpy((char *)wifi_config.ap.ssid, ssid, sizeof(wifi_config.ap.ssid));
        wifi_config.ap.ssid_len = strlen(ssid);
    } else {
        strlcpy((char *)wifi_config.ap.ssid, "Reticulum-Lifesaver", sizeof(wifi_config.ap.ssid));
        wifi_config.ap.ssid_len = strlen("Reticulum-Lifesaver");
    }

    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_AP));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_AP, &wifi_config));
    ESP_ERROR_CHECK(esp_wifi_start());

    ESP_LOGI(TAG, "Wi-Fi 6 SoftAP started with SSID: %s", wifi_config.ap.ssid);

    // Spawn DNS captive redirection task
    xTaskCreate(dns_server_task, "dns_task", 4096, NULL, 5, NULL);

    return ESP_OK;
}
