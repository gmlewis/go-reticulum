/*
 * Copyright 2026 Glenn Lewis. All rights reserved.
 *
 * Use of this source code is governed by the Reticulum License
 * that can be found in the LICENSE file.
 */

#include <stdint.h>
#include "esp_rom_regi2c.h"

uint32_t phy_rom_phyFuns = 0;

void phy_pbus_xpd_rx_off(void) {
}

void phy_pbus_xpd_rx_on(void) {
}

void phy_i2c_writeReg_Mask(uint32_t block, uint32_t host_id, uint32_t reg_add, uint32_t msb, uint32_t lsb, uint32_t data) {
    esp_rom_regi2c_write_mask(block, host_id, reg_add, msb, lsb, data);
}
