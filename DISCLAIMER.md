# Legal, Safety, Emergency, and Medical Disclaimer

**PLEASE READ THIS DISCLAIMER CAREFULLY BEFORE USING ANY SOFTWARE, HARDWARE DESIGNS, OR DOCUMENTATION CONTAINED IN THIS REPOSITORY.**

---

### 1. NOT A CERTIFIED LIFE-SAFETY DEVICE / NO GUARANTEE OF RESCUE

The software and hardware designs in this repository (including, but not limited to, the **Go Reticulum Lifesaver (GRL)**, `cmd/grl`, `cmd/gorrcbot`, `cmd/gobot`, and associated Reticulum/LXMF implementations):

- **Operate on Unlicensed, Best-Effort Frequencies**: Transmissions occur over unlicensed, low-power Industrial, Scientific, and Medical (ISM) radio bands (such as 868 MHz / 915 MHz LoRa) and ad-hoc wireless networks. Communications are subject to packet loss, radio frequency interference, terrain blockage, antenna limitations, battery depletion, and complete network unavailability.
- **NO Guaranteed Delivery or Acknowledgement**: Delivery of any message, emergency beacon, packet, or distress call (`/sos`) is **never guaranteed**.
- **NOT Connected to Official Emergency Dispatch**: This system is **NOT** connected to official 911/112 Public Safety Answering Points (PSAPs), government emergency services, civil defense agencies, or COSPAS-SARSAT search-and-rescue satellites.
- **NOT a Certified Distress Beacon**: This system is **NOT** a certified Emergency Position Indicating Radio Beacon (EPIRB), Personal Locator Beacon (PLB), or certified commercial satellite emergency notification device (such as Garmin inReach® or SPOT®).
- **DO NOT RELY ON THIS SYSTEM FOR LIFE SAFETY**: Under no circumstances should this system be used as a primary, sole, or dependable means of summoning emergency rescue, medical assistance, or disaster evacuation. Always carry certified emergency signaling devices and register travel plans with appropriate authorities.

---

### 2. NOT A CERTIFIED MEDICAL DEVICE / INFORMATIONAL DECISION SUPPORT ONLY

The medical reference protocols, first-aid decision cards, cold water survival estimates, and triage guidelines provided within this software (including, but not limited to, the `med` and `firstaid` commands):

- **Informational & Educational Reference Only**: Content is compiled from general public wilderness medicine guidelines and is intended solely for offline educational and informational reference in remote situations where professional care is unavailable.
- **NOT Professional Medical Advice**: The software does **NOT** provide medical diagnosis, clinical prognosis, prescription, or certified medical treatment.
- **NO Patient-Provider Relationship**: Use of this software does not create a doctor-patient, provider-patient, or first-responder duty or relationship of any kind.
- **Always Seek Professional Medical Attention**: In any emergency, immediately seek professional medical assistance from licensed medical personnel as soon as possible.

---

### 3. NOT FOR PRIMARY NAVIGATION / DATA ACCURACY DISCLAIMER

The navigation, geodetic, ephemeris, and radio-frequency guidance provided by this software (including, but not limited to, `/whereami`, Open Location Code (OLC) Plus Codes, Maidenhead grid locators, astronomical calculations (`sun`, `moon`), magnetic declination estimates (WMM), offshore weather buoy coordinates, tide stations, and cellular/repeater tower catalogs (`tower near`)):

- **Subject to Inaccuracy and Drift**: GNSS/GPS fixes may suffer from multipath distortion, atmospheric degradation, jamming, or loss of lock. Electronic compasses and magnetic declination models are subject to local magnetic anomalies, tilt errors, and calibration drift. Astronomical and tidal approximations may differ from local observed conditions.
- **Static Catalogs May Be Stale or Inaccurate**: Tower, repeater, buoy, and airfield databases are static reference snapshots and may not reflect decommissioned sites, changed frequencies, CTCSS tone modifications, or temporary outages.
- **NOT for Primary Navigation**: Never use this system as your sole or primary tool for maritime, aeronautical, mountaineering, or wilderness navigation. Always carry dedicated primary navigation equipment, including official up-to-date topographical paper charts, a calibrated magnetic lensatic compass, and an altimeter.

---

### 4. RF AND REGULATORY COMPLIANCE

Radio frequency transmission equipment constructed or operated using software or hardware specifications from this repository must strictly comply with local, national, and international telecommunications regulations (such as FCC Part 15 / Part 97 in the United States, ETSI in the European Union, etc.):

- **Operator Responsibility**: The operator bears sole responsibility for verifying frequency allocations, maximum effective radiated power (ERP), duty cycle limits, channel bandwidth, antenna gain, and amateur radio licensing requirements.
- **NO Representation of Compliance**: The authors and contributors make no representations or warranties that any configuration complies with radio transmission regulations in your jurisdiction.

---

### 5. HARDWARE AND BATTERY HAZARDS

Hardware prototypes, breadboards, printed circuit boards (PCBs), and battery systems described in documentation (including lithium-ion 18650 cells, charging modules, and antennas):

- **Fire and Explosion Risk**: Lithium-ion batteries carry inherent risks of thermal runaway, fire, toxic gas release, and explosion if short-circuited, punctured, overcharged, overheated, or improperly managed.
- **User Assumes All Fabrication Risks**: The user assumes all risks associated with soldering, assembly, component sourcing, wiring, power supplies, and field deployment.

---

### 6. EXPRESS ASSUMPTION OF RISK AND TOTAL RELEASE OF LIABILITY

**TO THE MAXIMUM EXTENT PERMITTED BY APPLICABLE LAW:**

1. **AS-IS PROVISION**: All software, source code, hardware designs, firmware, datasets, and documentation are provided "AS IS" and "AS AVAILABLE," WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, ACCURACY, COMPLETENESS, RELIABILITY, TITLE, OR NON-INFRINGEMENT.
2. **TOTAL RELEASE OF LIABILITY**: IN NO EVENT SHALL THE AUTHORS, CONTRIBUTORS, COPYRIGHT HOLDERS, OR DISTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, PUNITIVE, CONSEQUENTIAL, OR COMPENSATORY DAMAGES WHATSOEVER (INCLUDING, BUT NOT LIMITED TO, LOSS OF LIFE, PERSONAL INJURY, BODILY HARM, ILLNESS, PAIN AND SUFFERING, EMOTIONAL DISTRESS, WRONGFUL DEATH, PROPERTY DAMAGE, LOSS OF RESCUE OPPORTUNITY, SEARCH-AND-RESCUE EXPENSES, LOSS OF DATA, EQUIPMENT FAILURE, OR BUSINESS INTERRUPTION), REGARDLESS OF CAUSE AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR GROSS NEGLIGENCE), ARISING IN ANY WAY OUT OF THE USE OF, RECONSTRUCTION OF, OR RELIANCE UPON THIS SOFTWARE, HARDWARE, DATA, OR DOCUMENTATION, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
3. **INDEMNIFICATION**: BY USING, BUILDING, RUNNING, OR DISTRIBUTING THIS SYSTEM, YOU AGREE TO INDEMNIFY, DEFEND, AND HOLD HARMLESS THE AUTHORS AND CONTRIBUTORS FROM ANY AND ALL CLAIMS, LAWSUITS, DEMANDS, LIABILITIES, OR COSTS ARISING FROM YOUR USE, MISUSE, OR DEPLOYMENT OF THIS SYSTEM.
