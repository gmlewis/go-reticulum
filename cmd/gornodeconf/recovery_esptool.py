#!/usr/bin/env python3

from __future__ import division,print_function
_AO='<BBBBI'
_AN='ESP32-C3'
_AM='ESP32-S3(beta3)'
_AL='ESP32-S3(beta2)'
_AK='ESP32-S3'
_AJ='MEM_INTERNAL'
_AI='ESP32-S2'
_AH='chip_id'
_AG='ESP32'
_AF='Embedded Flash'
_AE='ESP8285'
_AD='4MB-c1'
_AC='2MB-c1'
_AB='utf-8'
_AA='Took %.2fs to erase flash block'
_A9='usb_reset'
_A8='raw'
_A7='80m'
_A6='20m'
_A5='26m'
_A4='dout'
_A3='dio'
_A2='qout'
_A1='detect'
_A0='Valid key block numbers must be in range 0-5'
_z='IROM_MASK'
_y='%s (revision %d)'
_x='RTC_DATA'
_w='EXTRAM_DATA'
_v=', '
_u=b'\xdb'
_t='ESP8266'
_s='16MB'
_r='8MB'
_q='512KB'
_p='256KB'
_o='40m'
_n='qio'
_m=b'\xff'
_l='>II'
_k='RTC_IRAM'
_j='BYTE_ACCESSIBLE'
_i='RTC_DRAM'
_h='DROM'
_g='PADDING'
_f='WiFi'
_e='no_reset'
_d='no_reset_no_sync'
_c='2MB'
_b='esp32c6beta'
_a='esp32c3'
_Z='esp32s3beta3'
_Y='esp32s3beta2'
_X='esp32s2'
_W='esp32'
_V='esp8266'
_U='IRAM'
_T='DRAM'
_S='4MB'
_R='1MB'
_Q='auto'
_P='1'
_O='rb'
_N='IROM'
_M='<II'
_L=b'\xc0'
_K='default_reset'
_J='wb'
_I=b'\x00'
_H='<I'
_G='2'
_F=b''
_E='<IIII'
_D='keep'
_C=True
_B=False
_A=None
import argparse,base64,binascii,copy,hashlib,inspect,io,itertools,os,shlex,string,struct,sys,time,zlib
try:import serial
except ImportError:print('Pyserial is not installed for %s. Check the README for installation instructions.'%sys.executable);raise
try:
    if'serialization'in serial.__doc__ and'deserialization'in serial.__doc__:raise ImportError("\nesptool.py depends on pyserial, but there is a conflict with a currently installed package named 'serial'.\n\nYou may be able to work around this by 'pip uninstall serial; pip install pyserial' but this may break other installed Python software that depends on 'serial'.\n\nThere is no good fix for this right now, apart from configuring virtualenvs. See https://github.com/espressif/esptool/issues/269#issuecomment-385298196 for discussion of the underlying issue(s).")
except TypeError:pass
try:import serial.tools.list_ports as list_ports
except ImportError:print('The installed version (%s) of pyserial appears to be too old for esptool.py (Python interpreter %s). Check the README for installation instructions.'%(sys.VERSION,sys.executable));raise
except Exception:
    if sys.platform=='darwin':list_ports=_A
    else:raise
__version__='3.1'
MAX_UINT32=4294967295
MAX_UINT24=16777215
DEFAULT_TIMEOUT=3
START_FLASH_TIMEOUT=20
CHIP_ERASE_TIMEOUT=120
MAX_TIMEOUT=CHIP_ERASE_TIMEOUT*2
SYNC_TIMEOUT=0.1
MD5_TIMEOUT_PER_MB=8
ERASE_REGION_TIMEOUT_PER_MB=30
ERASE_WRITE_TIMEOUT_PER_MB=40
MEM_END_ROM_TIMEOUT=0.05
DEFAULT_SERIAL_WRITE_TIMEOUT=10
DEFAULT_CONNECT_ATTEMPTS=7
def timeout_per_mb(seconds_per_mb,size_bytes):
    ' Scales timeouts which are size-specific ';result=seconds_per_mb*(size_bytes/1000000.0)
    if result<DEFAULT_TIMEOUT:return DEFAULT_TIMEOUT
    return result
def _chip_to_rom_loader(chip):return{_V:ESP8266ROM,_W:ESP32ROM,_X:ESP32S2ROM,_Y:ESP32S3BETA2ROM,_Z:ESP32S3BETA3ROM,_a:ESP32C3ROM,_b:ESP32C6BETAROM}[chip]
def get_default_connected_device(serial_list,port,connect_attempts,initial_baud,chip=_Q,trace=_B,before=_K):
    _esp=_A
    for each_port in reversed(serial_list):
        print('Serial port %s'%each_port)
        try:
            if chip==_Q:_esp=ESPLoader.detect_chip(each_port,initial_baud,before,trace,connect_attempts)
            else:chip_class=_chip_to_rom_loader(chip);_esp=chip_class(each_port,initial_baud,trace);_esp.connect(before,connect_attempts)
            break
        except (FatalError,OSError)as err:
            if port is not _A:raise
            print('%s failed to connect: %s'%(each_port,err));_esp=_A
    return _esp
DETECTED_FLASH_SIZES={18:_p,19:_q,20:_R,21:_c,22:_S,23:_r,24:_s,25:'32MB',26:'64MB'}
def check_supported_function(func,check_func):
    "\n    Decorator implementation that wraps a check around an ESPLoader\n    bootloader function to check if it's supported.\n\n    This is used to capture the multidimensional differences in\n    functionality between the ESP8266 & ESP32/32S2/32S3/32C3 ROM loaders, and the\n    software stub that runs on both. Not possible to do this cleanly\n    via inheritance alone.\n    "
    def inner(*args,**kwargs):
        obj=args[0]
        if check_func(obj):return func(*args,**kwargs)
        else:raise NotImplementedInROMError(obj,func)
    return inner
def stub_function_only(func):' Attribute for a function only supported in the software stub loader ';return check_supported_function(func,lambda o:o.IS_STUB)
def stub_and_esp32_function_only(func):' Attribute for a function only supported by software stubs or ESP32/32S2/32S3/32C3 ROM ';return check_supported_function(func,lambda o:o.IS_STUB or isinstance(o,ESP32ROM))
PYTHON2=sys.version_info[0]<3
if PYTHON2:
    def byte(bitstr,index):return ord(bitstr[index])
else:
    def byte(bitstr,index):return bitstr[index]
try:basestring
except NameError:basestring=str
def print_overwrite(message,last_line=_B):
    " Print a message, overwriting the currently printed line.\n\n    If last_line is False, don't append a newline at the end (expecting another subsequent call will overwrite this one.)\n\n    After a sequence of calls with last_line=False, call once with last_line=True.\n\n    If output is not a TTY (for example redirected a pipe), no overwriting happens and this function is the same as print().\n    "
    if sys.stdout.isatty():print('\r%s'%message,end='\n'if last_line else'')
    else:print(message)
def _mask_to_shift(mask):
    ' Return the index of the least significant bit in the mask ';shift=0
    while mask&1==0:shift+=1;mask>>=1
    return shift
def esp8266_function_only(func):' Attribute for a function only supported on ESP8266 ';return check_supported_function(func,lambda o:o.CHIP_NAME==_t)
class ESPLoader:
    " Base class providing access to ESP ROM & software stub bootloaders.\n    Subclasses provide ESP8266 & ESP32 specific functionality.\n\n    Don't instantiate this base class directly, either instantiate a subclass or\n    call ESPLoader.detect_chip() which will interrogate the chip and return the\n    appropriate subclass instance.\n\n    ";CHIP_NAME='Espressif device';IS_STUB=_B;DEFAULT_PORT='/dev/ttyUSB0';ESP_FLASH_BEGIN=2;ESP_FLASH_DATA=3;ESP_FLASH_END=4;ESP_MEM_BEGIN=5;ESP_MEM_END=6;ESP_MEM_DATA=7;ESP_SYNC=8;ESP_WRITE_REG=9;ESP_READ_REG=10;ESP_SPI_SET_PARAMS=11;ESP_SPI_ATTACH=13;ESP_READ_FLASH_SLOW=14;ESP_CHANGE_BAUDRATE=15;ESP_FLASH_DEFL_BEGIN=16;ESP_FLASH_DEFL_DATA=17;ESP_FLASH_DEFL_END=18;ESP_SPI_FLASH_MD5=19;ESP_GET_SECURITY_INFO=20;ESP_ERASE_FLASH=208;ESP_ERASE_REGION=209;ESP_READ_FLASH=210;ESP_RUN_USER_CODE=211;ESP_FLASH_ENCRYPT_DATA=212;ROM_INVALID_RECV_MSG=5;ESP_RAM_BLOCK=6144;FLASH_WRITE_SIZE=1024;ESP_ROM_BAUD=115200;ESP_IMAGE_MAGIC=233;ESP_CHECKSUM_MAGIC=239;FLASH_SECTOR_SIZE=4096;UART_DATE_REG_ADDR=1610612856;CHIP_DETECT_MAGIC_REG_ADDR=1073745920;UART_CLKDIV_MASK=1048575;IROM_MAP_START=1075838976;IROM_MAP_END=1076887552;STATUS_BYTES_LENGTH=2;sync_stub_detected=_B;USB_JTAG_SERIAL_PID=4097
    def __init__(self,port=DEFAULT_PORT,baud=ESP_ROM_BAUD,trace_enabled=_B):
        "Base constructor for ESPLoader bootloader interaction\n\n        Don't call this constructor, either instantiate ESP8266ROM\n        or ESP32ROM, or use ESPLoader.detect_chip().\n\n        This base class has all of the instance methods for bootloader\n        functionality supported across various chips & stub\n        loaders. Subclasses replace the functions they don't support\n        with ones which throw NotImplementedInROMError().\n\n        ";self.secure_download_mode=_B
        if isinstance(port,basestring):self._port=serial.serial_for_url(port)
        else:self._port=port
        self._slip_reader=slip_reader(self._port,self.trace);self._set_port_baudrate(baud);self._trace_enabled=trace_enabled
        try:self._port.write_timeout=DEFAULT_SERIAL_WRITE_TIMEOUT
        except NotImplementedError:self._port.write_timeout=_A
    @property
    def serial_port(self):return self._port.port
    def _set_port_baudrate(self,baud):
        try:self._port.baudrate=baud
        except IOError:raise FatalError('Failed to set baud rate %d. The driver may not support this rate.'%baud)
    @staticmethod
    def detect_chip(port=DEFAULT_PORT,baud=ESP_ROM_BAUD,connect_mode=_K,trace_enabled=_B,connect_attempts=DEFAULT_CONNECT_ATTEMPTS):
        " Use serial access to detect the chip type.\n\n        We use the UART's datecode register for this, it's mapped at\n        the same address on ESP8266 & ESP32 so we can use one\n        memory read and compare to the datecode register for each chip\n        type.\n\n        This routine automatically performs ESPLoader.connect() (passing\n        connect_mode parameter) as part of querying the chip.\n        ";detect_port=ESPLoader(port,baud,trace_enabled=trace_enabled);detect_port.connect(connect_mode,connect_attempts,detecting=_C)
        try:
            print('Detecting chip type...',end='');sys.stdout.flush();chip_magic_value=detect_port.read_reg(ESPLoader.CHIP_DETECT_MAGIC_REG_ADDR)
            for cls in [ESP8266ROM,ESP32ROM,ESP32S2ROM,ESP32S3BETA2ROM,ESP32S3BETA3ROM,ESP32C3ROM,ESP32C6BETAROM]:
                if chip_magic_value in cls.CHIP_DETECT_MAGIC_VALUE:
                    inst=cls(detect_port._port,baud,trace_enabled=trace_enabled);inst._post_connect();print(' %s'%inst.CHIP_NAME,end='')
                    if detect_port.sync_stub_detected:inst=inst.STUB_CLASS(inst);inst.sync_stub_detected=_C
                    return inst
        except UnsupportedCommandError:raise FatalError('Unsupported Command Error received. Probably this means Secure Download Mode is enabled, autodetection will not work. Need to manually specify the chip.')
        finally:print('')
        raise FatalError('Unexpected CHIP magic value 0x%08x. Failed to autodetect chip type.'%chip_magic_value)
    ' Read a SLIP packet from the serial port '
    def read(self):return next(self._slip_reader)
    ' Write bytes to the serial port while performing SLIP escaping '
    def write(self,packet):buf=_L+packet.replace(_u,b'\xdb\xdd').replace(_L,b'\xdb\xdc')+_L;self.trace('Write %d bytes: %s',len(buf),HexFormatter(buf));self._port.write(buf)
    def trace(self,message,*format_args):
        if self._trace_enabled:
            now=time.time()
            try:delta=now-self._last_trace
            except AttributeError:delta=0.0
            self._last_trace=now;prefix='TRACE +%.3f '%delta;print(prefix+message%format_args)
    ' Calculate checksum of a blob, as it is defined by the ROM '
    @staticmethod
    def checksum(data,state=ESP_CHECKSUM_MAGIC):
        for b in data:
            if type(b)is int:state^=b
            else:state^=ord(b)
        return state
    ' Send a request and read the response '
    def command(self,op=_A,data=_F,chk=0,wait_response=_C,timeout=DEFAULT_TIMEOUT):
        saved_timeout=self._port.timeout;new_timeout=min(timeout,MAX_TIMEOUT)
        if new_timeout!=saved_timeout:self._port.timeout=new_timeout
        try:
            if op is not _A:self.trace('command op=0x%02x data len=%s wait_response=%d timeout=%.3f data=%s',op,len(data),1 if wait_response else 0,timeout,HexFormatter(data));pkt=struct.pack(b'<BBHI',0,op,len(data),chk)+data;self.write(pkt)
            if not wait_response:return
            for retry in range(100):
                p=self.read()
                if len(p)<8:continue
                resp,op_ret,len_ret,val=struct.unpack('<BBHI',p[:8])
                if resp!=1:continue
                data=p[8:]
                if op is _A or op_ret==op:return val,data
                if byte(data,0)!=0 and byte(data,1)==self.ROM_INVALID_RECV_MSG:self.flush_input();raise UnsupportedCommandError(self,op)
        finally:
            if new_timeout!=saved_timeout:self._port.timeout=saved_timeout
        raise FatalError("Response doesn't match request")
    def check_command(self,op_description,op=_A,data=_F,chk=0,timeout=DEFAULT_TIMEOUT):
        '\n        Execute a command with \'command\', check the result code and throw an appropriate\n        FatalError if it fails.\n\n        Returns the "result" of a successful command.\n        ';val,data=self.command(op,data,chk,timeout=timeout)
        if len(data)<self.STATUS_BYTES_LENGTH:raise FatalError('Failed to %s. Only got %d byte status response.'%(op_description,len(data)))
        status_bytes=data[-self.STATUS_BYTES_LENGTH:]
        if byte(status_bytes,0)!=0:raise FatalError.WithResult('Failed to %s'%op_description,status_bytes)
        if len(data)>self.STATUS_BYTES_LENGTH:return data[:-self.STATUS_BYTES_LENGTH]
        else:return val
    def flush_input(self):self._port.flushInput();self._slip_reader=slip_reader(self._port,self.trace)
    def sync(self):
        val,_=self.command(self.ESP_SYNC,b'\x07\x07\x12 '+32*b'U',timeout=SYNC_TIMEOUT);self.sync_stub_detected=val==0
        for _ in range(7):val,_=self.command();self.sync_stub_detected&=val==0
    def _setDTR(self,state):self._port.setDTR(state)
    def _setRTS(self,state):self._port.setRTS(state);self._port.setDTR(self._port.dtr)
    def _get_pid(self):
        A='/dev/'
        if list_ports is _A:print("\nListing all serial ports is currently not available. Can't get device PID.");return
        active_port=self._port.port
        if not active_port.startswith(('COM',A)):print('\nDevice PID identification is only supported on COM and /dev/ serial ports.');return
        if active_port.startswith(A)and os.path.islink(active_port):active_port=os.path.realpath(active_port)
        ports=list_ports.comports()
        for p in ports:
            if p.device==active_port:return p.pid
        print('\nFailed to get PID of a device on {}, using standard reset sequence.'.format(active_port))
    def bootloader_reset(self,esp32r0_delay=_B,usb_jtag_serial=_B):
        ' Issue a reset-to-bootloader, with esp32r0 workaround options\n        and USB-JTAG-Serial custom reset sequence option\n        '
        if usb_jtag_serial:self._setRTS(_B);self._setDTR(_B);time.sleep(0.1);self._setDTR(_C);self._setRTS(_B);time.sleep(0.1);self._setRTS(_C);self._setDTR(_B);self._setRTS(_C);time.sleep(0.1);self._setDTR(_B);self._setRTS(_B)
        else:
            self._setDTR(_B);self._setRTS(_C);time.sleep(0.1)
            if esp32r0_delay:time.sleep(1.2)
            self._setDTR(_C);self._setRTS(_B)
            if esp32r0_delay:time.sleep(0.4)
            time.sleep(0.05);self._setDTR(_B)
    def _connect_attempt(self,mode=_K,esp32r0_delay=_B,usb_jtag_serial=_B):
        ' A single connection attempt, with esp32r0 workaround options ';last_error=_A
        if mode==_d:return last_error
        if mode!=_e:self.bootloader_reset(esp32r0_delay,usb_jtag_serial)
        for _ in range(5):
            try:self.flush_input();self._port.flushOutput();self.sync();return _A
            except FatalError as e:
                if esp32r0_delay:print('_',end='')
                else:print('.',end='')
                sys.stdout.flush();time.sleep(0.05);last_error=e
        return last_error
    def get_memory_region(self,name):
        " Returns a tuple of (start, end) for the memory map entry with the given name, or None if it doesn't exist\n        "
        try:return[(start,end)for(start,end,n)in self.MEMORY_MAP if n==name][0]
        except IndexError:return _A
    def connect(self,mode=_K,attempts=DEFAULT_CONNECT_ATTEMPTS,detecting=_B):
        ' Try connecting repeatedly until successful, or giving up '
        if mode in[_e,_d]:print('WARNING: Pre-connection option "{}" was selected.'.format(mode),'Connection may fail if the chip is not in bootloader or flasher stub mode.')
        print('Connecting...',end='');sys.stdout.flush();last_error=_A;usb_jtag_serial=mode==_A9 or self._get_pid()==self.USB_JTAG_SERIAL_PID
        try:
            for _ in range(attempts)if attempts>0 else itertools.count():
                last_error=self._connect_attempt(mode=mode,esp32r0_delay=_B,usb_jtag_serial=usb_jtag_serial)
                if last_error is _A:break
                last_error=self._connect_attempt(mode=mode,esp32r0_delay=_C,usb_jtag_serial=usb_jtag_serial)
                if last_error is _A:break
        finally:print('')
        if last_error is not _A:raise FatalError('Failed to connect to %s: %s'%(self.CHIP_NAME,last_error))
        if not detecting:
            try:
                chip_magic_value=self.read_reg(ESPLoader.CHIP_DETECT_MAGIC_REG_ADDR)
                if chip_magic_value not in self.CHIP_DETECT_MAGIC_VALUE:
                    actually=_A
                    for cls in [ESP8266ROM,ESP32ROM,ESP32S2ROM,ESP32S3BETA2ROM,ESP32S3BETA3ROM,ESP32C3ROM]:
                        if chip_magic_value in cls.CHIP_DETECT_MAGIC_VALUE:actually=cls;break
                    if actually is _A:print("WARNING: This chip doesn't appear to be a %s (chip magic value 0x%08x). Probably it is unsupported by this version of esptool."%(self.CHIP_NAME,chip_magic_value))
                    else:raise FatalError('This chip is %s not %s. Wrong --chip argument?'%(actually.CHIP_NAME,self.CHIP_NAME))
            except UnsupportedCommandError:self.secure_download_mode=_C
            self._post_connect()
    def _post_connect(self):'\n        Additional initialization hook, may be overridden by the chip-specific class.\n        Gets called after connect, and after auto-detection.\n        '
    def read_reg(self,addr,timeout=DEFAULT_TIMEOUT):
        ' Read memory address in target ';val,data=self.command(self.ESP_READ_REG,struct.pack(_H,addr),timeout=timeout)
        if byte(data,0)!=0:raise FatalError.WithResult('Failed to read register address %08x'%addr,data)
        return val
    ' Write to memory address in target '
    def write_reg(self,addr,value,mask=4294967295,delay_us=0,delay_after_us=0):
        command=struct.pack(_E,addr,value,mask,delay_us)
        if delay_after_us>0:command+=struct.pack(_E,self.UART_DATE_REG_ADDR,0,0,delay_after_us)
        return self.check_command('write target memory',self.ESP_WRITE_REG,command)
    def update_reg(self,addr,mask,new_val):" Update register at 'addr', replace the bits masked out by 'mask'\n        with new_val. new_val is shifted left to match the LSB of 'mask'\n\n        Returns just-written value of register.\n        ";shift=_mask_to_shift(mask);val=self.read_reg(addr);val&=~ mask;val|=new_val<<shift&mask;self.write_reg(addr,val);return val
    ' Start downloading an application image to RAM '
    def mem_begin(self,size,blocks,blocksize,offset):
        B='text_start';A='data_start'
        if self.IS_STUB:
            stub=self.STUB_CODE;load_start=offset;load_end=offset+size
            for (start,end) in [(stub[A],stub[A]+len(stub['data'])),(stub[B],stub[B]+len(stub['text']))]:
                if load_start<end and load_end>start:raise FatalError("Software loader is resident at 0x%08x-0x%08x. Can't load binary at overlapping address range 0x%08x-0x%08x. Either change binary loading address, or use the --no-stub option to disable the software loader."%(start,end,load_start,load_end))
        return self.check_command('enter RAM download mode',self.ESP_MEM_BEGIN,struct.pack(_E,size,blocks,blocksize,offset))
    ' Send a block of an image to RAM '
    def mem_block(self,data,seq):return self.check_command('write to target RAM',self.ESP_MEM_DATA,struct.pack(_E,len(data),seq,0,0)+data,self.checksum(data))
    ' Leave download mode and run the application '
    def mem_finish(self,entrypoint=0):
        timeout=DEFAULT_TIMEOUT if self.IS_STUB else MEM_END_ROM_TIMEOUT;data=struct.pack(_M,int(entrypoint==0),entrypoint)
        try:return self.check_command('leave RAM download mode',self.ESP_MEM_END,data=data,timeout=timeout)
        except FatalError:
            if self.IS_STUB:raise
            pass
    ' Start downloading to Flash (performs an erase)\n\n    Returns number of blocks (of size self.FLASH_WRITE_SIZE) to write.\n    '
    def flash_begin(self,size,offset,begin_rom_encrypted=_B):
        num_blocks=(size+self.FLASH_WRITE_SIZE-1)//self.FLASH_WRITE_SIZE;erase_size=self.get_erase_size(offset,size);t=time.time()
        if self.IS_STUB:timeout=DEFAULT_TIMEOUT
        else:timeout=timeout_per_mb(ERASE_REGION_TIMEOUT_PER_MB,size)
        params=struct.pack(_E,erase_size,num_blocks,self.FLASH_WRITE_SIZE,offset)
        if isinstance(self,(ESP32S2ROM,ESP32S3BETA2ROM,ESP32S3BETA3ROM,ESP32C3ROM,ESP32C6BETAROM))and not self.IS_STUB:params+=struct.pack(_H,1 if begin_rom_encrypted else 0)
        self.check_command('enter Flash download mode',self.ESP_FLASH_BEGIN,params,timeout=timeout)
        if size!=0 and not self.IS_STUB:print(_AA%(time.time()-t))
        return num_blocks
    ' Write block to flash '
    def flash_block(self,data,seq,timeout=DEFAULT_TIMEOUT):self.check_command('write to target Flash after seq %d'%seq,self.ESP_FLASH_DATA,struct.pack(_E,len(data),seq,0,0)+data,self.checksum(data),timeout=timeout)
    ' Encrypt before writing to flash '
    def flash_encrypt_block(self,data,seq,timeout=DEFAULT_TIMEOUT):
        if isinstance(self,(ESP32S2ROM,ESP32C3ROM))and not self.IS_STUB:return self.flash_block(data,seq,timeout)
        self.check_command('Write encrypted to target Flash after seq %d'%seq,self.ESP_FLASH_ENCRYPT_DATA,struct.pack(_E,len(data),seq,0,0)+data,self.checksum(data),timeout=timeout)
    ' Leave flash mode and run/reboot '
    def flash_finish(self,reboot=_B):pkt=struct.pack(_H,int(not reboot));self.check_command('leave Flash mode',self.ESP_FLASH_END,pkt)
    ' Run application code in flash '
    def run(self,reboot=_B):self.flash_begin(0,0);self.flash_finish(reboot)
    ' Read SPI flash manufacturer and device id '
    def flash_id(self):SPIFLASH_RDID=159;return self.run_spiflash_command(SPIFLASH_RDID,_F,24)
    def get_security_info(self):res=self.check_command('get security info',self.ESP_GET_SECURITY_INFO,_F);res=struct.unpack('<IBBBBBBBB',res);flags,flash_crypt_cnt,key_purposes=res[0],res[1],res[2:];return flags,flash_crypt_cnt,key_purposes
    @classmethod
    def parse_flash_size_arg(cls,arg):
        try:return cls.FLASH_SIZES[arg]
        except KeyError:raise FatalError("Flash size '%s' is not supported by this chip type. Supported sizes: %s"%(arg,_v.join(cls.FLASH_SIZES.keys())))
    def run_stub(self,stub=_A):
        if stub is _A:stub=self.STUB_CODE
        if self.sync_stub_detected:print('Stub is already running. No upload is necessary.');return self.STUB_CLASS(self)
        print('Uploading stub...')
        for field in ['text','data']:
            if field in stub:
                offs=stub[field+'_start'];length=len(stub[field]);blocks=(length+self.ESP_RAM_BLOCK-1)//self.ESP_RAM_BLOCK;self.mem_begin(length,blocks,self.ESP_RAM_BLOCK,offs)
                for seq in range(blocks):from_offs=seq*self.ESP_RAM_BLOCK;to_offs=from_offs+self.ESP_RAM_BLOCK;self.mem_block(stub[field][from_offs:to_offs],seq)
        print('Running stub...');self.mem_finish(stub['entry']);p=self.read()
        if p!=b'OHAI':raise FatalError('Failed to start stub. Unexpected response: %s'%p)
        print('Stub running...');return self.STUB_CLASS(self)
    @stub_and_esp32_function_only
    def flash_defl_begin(self,size,compsize,offset):
        ' Start downloading compressed data to Flash (performs an erase)\n\n        Returns number of blocks (size self.FLASH_WRITE_SIZE) to write.\n        ';num_blocks=(compsize+self.FLASH_WRITE_SIZE-1)//self.FLASH_WRITE_SIZE;erase_blocks=(size+self.FLASH_WRITE_SIZE-1)//self.FLASH_WRITE_SIZE;t=time.time()
        if self.IS_STUB:write_size=size;timeout=DEFAULT_TIMEOUT
        else:write_size=erase_blocks*self.FLASH_WRITE_SIZE;timeout=timeout_per_mb(ERASE_REGION_TIMEOUT_PER_MB,write_size)
        print('Compressed %d bytes to %d...'%(size,compsize));params=struct.pack(_E,write_size,num_blocks,self.FLASH_WRITE_SIZE,offset)
        if isinstance(self,(ESP32S2ROM,ESP32S3BETA2ROM,ESP32S3BETA3ROM,ESP32C3ROM,ESP32C6BETAROM))and not self.IS_STUB:params+=struct.pack(_H,0)
        self.check_command('enter compressed flash mode',self.ESP_FLASH_DEFL_BEGIN,params,timeout=timeout)
        if size!=0 and not self.IS_STUB:print(_AA%(time.time()-t))
        return num_blocks
    ' Write block to flash, send compressed '
    @stub_and_esp32_function_only
    def flash_defl_block(self,data,seq,timeout=DEFAULT_TIMEOUT):self.check_command('write compressed data to flash after seq %d'%seq,self.ESP_FLASH_DEFL_DATA,struct.pack(_E,len(data),seq,0,0)+data,self.checksum(data),timeout=timeout)
    ' Leave compressed flash mode and run/reboot '
    @stub_and_esp32_function_only
    def flash_defl_finish(self,reboot=_B):
        if not reboot and not self.IS_STUB:return
        pkt=struct.pack(_H,int(not reboot));self.check_command('leave compressed flash mode',self.ESP_FLASH_DEFL_END,pkt);self.in_bootloader=_B
    @stub_and_esp32_function_only
    def flash_md5sum(self,addr,size):
        timeout=timeout_per_mb(MD5_TIMEOUT_PER_MB,size);res=self.check_command('calculate md5sum',self.ESP_SPI_FLASH_MD5,struct.pack(_E,addr,size,0,0),timeout=timeout)
        if len(res)==32:return res.decode(_AB)
        elif len(res)==16:return hexify(res).lower()
        else:raise FatalError('MD5Sum command returned unexpected result: %r'%res)
    @stub_and_esp32_function_only
    def change_baud(self,baud):print('Changing baud rate to %d'%baud);second_arg=self._port.baudrate if self.IS_STUB else 0;self.command(self.ESP_CHANGE_BAUDRATE,struct.pack(_M,baud,second_arg));print('Changed.');self._set_port_baudrate(baud);time.sleep(0.05);self.flush_input()
    @stub_function_only
    def erase_flash(self):self.check_command('erase flash',self.ESP_ERASE_FLASH,timeout=CHIP_ERASE_TIMEOUT)
    @stub_function_only
    def erase_region(self,offset,size):
        if offset%self.FLASH_SECTOR_SIZE!=0:raise FatalError('Offset to erase from must be a multiple of 4096')
        if size%self.FLASH_SECTOR_SIZE!=0:raise FatalError('Size of data to erase must be a multiple of 4096')
        timeout=timeout_per_mb(ERASE_REGION_TIMEOUT_PER_MB,size);self.check_command('erase region',self.ESP_ERASE_REGION,struct.pack(_M,offset,size),timeout=timeout)
    def read_flash_slow(self,offset,length,progress_fn):raise NotImplementedInROMError(self,self.read_flash_slow)
    def read_flash(self,offset,length,progress_fn=_A):
        if not self.IS_STUB:return self.read_flash_slow(offset,length,progress_fn)
        self.check_command('read flash',self.ESP_READ_FLASH,struct.pack(_E,offset,length,self.FLASH_SECTOR_SIZE,64));data=_F
        while len(data)<length:
            p=self.read();data+=p
            if len(data)<length and len(p)<self.FLASH_SECTOR_SIZE:raise FatalError('Corrupt data, expected 0x%x bytes but received 0x%x bytes'%(self.FLASH_SECTOR_SIZE,len(p)))
            self.write(struct.pack(_H,len(data)))
            if progress_fn and(len(data)%1024==0 or len(data)==length):progress_fn(len(data),length)
        if progress_fn:progress_fn(len(data),length)
        if len(data)>length:raise FatalError('Read more than expected')
        digest_frame=self.read()
        if len(digest_frame)!=16:raise FatalError('Expected digest, got: %s'%hexify(digest_frame))
        expected_digest=hexify(digest_frame).upper();digest=hashlib.md5(data).hexdigest().upper()
        if digest!=expected_digest:raise FatalError('Digest mismatch: expected %s, got %s'%(expected_digest,digest))
        return data
    def flash_spi_attach(self,hspi_arg):
        'Send SPI attach command to enable the SPI flash pins\n\n        ESP8266 ROM does this when you send flash_begin, ESP32 ROM\n        has it as a SPI command.\n        ';arg=struct.pack(_H,hspi_arg)
        if not self.IS_STUB:is_legacy=0;arg+=struct.pack('BBBB',is_legacy,0,0,0)
        self.check_command('configure SPI flash pins',ESP32ROM.ESP_SPI_ATTACH,arg)
    def flash_set_parameters(self,size):'Tell the ESP bootloader the parameters of the chip\n\n        Corresponds to the "flashchip" data structure that the ROM\n        has in RAM.\n\n        \'size\' is in bytes.\n\n        All other flash parameters are currently hardcoded (on ESP8266\n        these are mostly ignored by ROM code, on ESP32 I\'m not sure.)\n        ';fl_id=0;total_size=size;block_size=64*1024;sector_size=4*1024;page_size=256;status_mask=65535;self.check_command('set SPI params',ESP32ROM.ESP_SPI_SET_PARAMS,struct.pack('<IIIIII',fl_id,total_size,block_size,sector_size,page_size,status_mask))
    def run_spiflash_command(self,spiflash_command,data=_F,read_bits=0):
        'Run an arbitrary SPI flash command.\n\n        This function uses the "USR_COMMAND" functionality in the ESP\n        SPI hardware, rather than the precanned commands supported by\n        hardware. So the value of spiflash_command is an actual command\n        byte, sent over the wire.\n\n        After writing command byte, writes \'data\' to MOSI and then\n        reads back \'read_bits\' of reply on MISO. Result is a number.\n        ';SPI_USR_COMMAND=1<<31;SPI_USR_MISO=1<<28;SPI_USR_MOSI=1<<27;base=self.SPI_REG_BASE;SPI_CMD_REG=base+0;SPI_USR_REG=base+self.SPI_USR_OFFS;SPI_USR1_REG=base+self.SPI_USR1_OFFS;SPI_USR2_REG=base+self.SPI_USR2_OFFS;SPI_W0_REG=base+self.SPI_W0_OFFS
        if self.SPI_MOSI_DLEN_OFFS is not _A:
            def set_data_lengths(mosi_bits,miso_bits):
                SPI_MOSI_DLEN_REG=base+self.SPI_MOSI_DLEN_OFFS;SPI_MISO_DLEN_REG=base+self.SPI_MISO_DLEN_OFFS
                if mosi_bits>0:self.write_reg(SPI_MOSI_DLEN_REG,mosi_bits-1)
                if miso_bits>0:self.write_reg(SPI_MISO_DLEN_REG,miso_bits-1)
        else:
            def set_data_lengths(mosi_bits,miso_bits):SPI_DATA_LEN_REG=SPI_USR1_REG;SPI_MOSI_BITLEN_S=17;SPI_MISO_BITLEN_S=8;mosi_mask=0 if mosi_bits==0 else mosi_bits-1;miso_mask=0 if miso_bits==0 else miso_bits-1;self.write_reg(SPI_DATA_LEN_REG,miso_mask<<SPI_MISO_BITLEN_S|mosi_mask<<SPI_MOSI_BITLEN_S)
        SPI_CMD_USR=1<<18;SPI_USR2_COMMAND_LEN_SHIFT=28
        if read_bits>32:raise FatalError('Reading more than 32 bits back from a SPI flash operation is unsupported')
        if len(data)>64:raise FatalError('Writing more than 64 bytes of data with one SPI command is unsupported')
        data_bits=len(data)*8;old_spi_usr=self.read_reg(SPI_USR_REG);old_spi_usr2=self.read_reg(SPI_USR2_REG);flags=SPI_USR_COMMAND
        if read_bits>0:flags|=SPI_USR_MISO
        if data_bits>0:flags|=SPI_USR_MOSI
        set_data_lengths(data_bits,read_bits);self.write_reg(SPI_USR_REG,flags);self.write_reg(SPI_USR2_REG,7<<SPI_USR2_COMMAND_LEN_SHIFT|spiflash_command)
        if data_bits==0:self.write_reg(SPI_W0_REG,0)
        else:
            data=pad_to(data,4,_I);words=struct.unpack('I'*(len(data)//4),data);next_reg=SPI_W0_REG
            for word in words:self.write_reg(next_reg,word);next_reg+=4
        self.write_reg(SPI_CMD_REG,SPI_CMD_USR)
        def wait_done():
            for _ in range(10):
                if self.read_reg(SPI_CMD_REG)&SPI_CMD_USR==0:return
            raise FatalError('SPI command did not complete in time')
        wait_done();status=self.read_reg(SPI_W0_REG);self.write_reg(SPI_USR_REG,old_spi_usr);self.write_reg(SPI_USR2_REG,old_spi_usr2);return status
    def read_status(self,num_bytes=2):
        'Read up to 24 bits (num_bytes) of SPI flash status register contents\n        via RDSR, RDSR2, RDSR3 commands\n\n        Not all SPI flash supports all three commands. The upper 1 or 2\n        bytes may be 0xFF.\n        ';SPIFLASH_RDSR=5;SPIFLASH_RDSR2=53;SPIFLASH_RDSR3=21;status=0;shift=0
        for cmd in [SPIFLASH_RDSR,SPIFLASH_RDSR2,SPIFLASH_RDSR3][0:num_bytes]:status+=self.run_spiflash_command(cmd,read_bits=8)<<shift;shift+=8
        return status
    def write_status(self,new_status,num_bytes=2,set_non_volatile=_B):
        'Write up to 24 bits (num_bytes) of new status register\n\n        num_bytes can be 1, 2 or 3.\n\n        Not all flash supports the additional commands to write the\n        second and third byte of the status register. When writing 2\n        bytes, esptool also sends a 16-byte WRSR command (as some\n        flash types use this instead of WRSR2.)\n\n        If the set_non_volatile flag is set, non-volatile bits will\n        be set as well as volatile ones (WREN used instead of WEVSR).\n\n        ';SPIFLASH_WRSR=1;SPIFLASH_WRSR2=49;SPIFLASH_WRSR3=17;SPIFLASH_WEVSR=80;SPIFLASH_WREN=6;SPIFLASH_WRDI=4;enable_cmd=SPIFLASH_WREN if set_non_volatile else SPIFLASH_WEVSR
        if num_bytes==2:self.run_spiflash_command(enable_cmd);self.run_spiflash_command(SPIFLASH_WRSR,struct.pack('<H',new_status))
        for cmd in [SPIFLASH_WRSR,SPIFLASH_WRSR2,SPIFLASH_WRSR3][0:num_bytes]:self.run_spiflash_command(enable_cmd);self.run_spiflash_command(cmd,struct.pack('B',new_status&255));new_status>>=8
        self.run_spiflash_command(SPIFLASH_WRDI)
    def get_crystal_freq(self):
        uart_div=self.read_reg(self.UART_CLKDIV_REG)&self.UART_CLKDIV_MASK;est_xtal=self._port.baudrate*uart_div/1000000.0/self.XTAL_CLK_DIVIDER;norm_xtal=40 if est_xtal>33 else 26
        if abs(norm_xtal-est_xtal)>1:print('WARNING: Detected crystal freq %.2fMHz is quite different to normalized freq %dMHz. Unsupported crystal in use?'%(est_xtal,norm_xtal))
        return norm_xtal
    def hard_reset(self):print('Hard resetting via RTS pin...');self._setRTS(_C);time.sleep(0.1);self._setRTS(_B)
    def soft_reset(self,stay_in_bootloader):
        if not self.IS_STUB:
            if stay_in_bootloader:return
            else:self.flash_begin(0,0);self.flash_finish(_B)
        elif stay_in_bootloader:self.flash_begin(0,0);self.flash_finish(_C)
        elif self.CHIP_NAME!=_t:raise FatalError('Soft resetting is currently only supported on ESP8266')
        else:self.command(self.ESP_RUN_USER_CODE,wait_response=_B)
class ESP8266ROM(ESPLoader):
    ' Access class for ESP8266 ROM bootloader\n    ';CHIP_NAME=_t;IS_STUB=_B;CHIP_DETECT_MAGIC_VALUE=[4293968129];ESP_OTP_MAC0=1072693328;ESP_OTP_MAC1=1072693332;ESP_OTP_MAC3=1072693340;SPI_REG_BASE=1610613248;SPI_USR_OFFS=28;SPI_USR1_OFFS=32;SPI_USR2_OFFS=36;SPI_MOSI_DLEN_OFFS=_A;SPI_MISO_DLEN_OFFS=_A;SPI_W0_OFFS=64;UART_CLKDIV_REG=1610612756;XTAL_CLK_DIVIDER=2;FLASH_SIZES={_q:0,_p:16,_R:32,_c:48,_S:64,_AC:80,_AD:96,_r:128,_s:144};BOOTLOADER_FLASH_OFFSET=0;MEMORY_MAP=[[1072693248,1072693264,'DPORT'],[1073643520,1073741824,_T],[1074790400,1074823168,_U],[1075843088,1076760592,_N]]
    def get_efuses(self):result=self.read_reg(1072693340)<<96;result|=self.read_reg(1072693336)<<64;result|=self.read_reg(1072693332)<<32;result|=self.read_reg(1072693328);return result
    def _get_flash_size(self,efuses):
        r0_4=efuses&1<<4!=0;r3_25=efuses&1<<121!=0;r3_26=efuses&1<<122!=0;r3_27=efuses&1<<123!=0
        if r0_4 and not r3_25:
            if not r3_27 and not r3_26:return 1
            elif not r3_27 and r3_26:return 2
        if not r0_4 and r3_25:
            if not r3_27 and not r3_26:return 2
            elif not r3_27 and r3_26:return 4
        return-1
    def get_chip_description(self):
        efuses=self.get_efuses();is_8285=efuses&(1<<4|1<<80)!=0
        if is_8285:flash_size=self._get_flash_size(efuses);max_temp=efuses&1<<5!=0;chip_name={1:'ESP8285H08'if max_temp else'ESP8285N08',2:'ESP8285H16'if max_temp else'ESP8285N16'}.get(flash_size,_AE);return chip_name
        return'ESP8266EX'
    def get_chip_features(self):
        features=[_f]
        if _AE in self.get_chip_description():features+=[_AF]
        return features
    def flash_spi_attach(self,hspi_arg):
        if self.IS_STUB:super(ESP8266ROM,self).flash_spi_attach(hspi_arg)
        else:self.flash_begin(0,0)
    def flash_set_parameters(self,size):
        if self.IS_STUB:super(ESP8266ROM,self).flash_set_parameters(size)
    def chip_id(self):' Read Chip ID from efuse - the equivalent of the SDK system_get_chip_id() function ';id0=self.read_reg(self.ESP_OTP_MAC0);id1=self.read_reg(self.ESP_OTP_MAC1);return id0>>24|(id1&MAX_UINT24)<<8
    def read_mac(self):
        ' Read MAC from OTP ROM ';mac0=self.read_reg(self.ESP_OTP_MAC0);mac1=self.read_reg(self.ESP_OTP_MAC1);mac3=self.read_reg(self.ESP_OTP_MAC3)
        if mac3!=0:oui=mac3>>16&255,mac3>>8&255,mac3&255
        elif mac1>>16&255==0:oui=24,254,52
        elif mac1>>16&255==1:oui=172,208,116
        else:raise FatalError('Unknown OUI')
        return oui+(mac1>>8&255,mac1&255,mac0>>24&255)
    def get_erase_size(self,offset,size):
        ' Calculate an erase size given a specific size in bytes.\n\n        Provides a workaround for the bootloader erase bug.';sectors_per_block=16;sector_size=self.FLASH_SECTOR_SIZE;num_sectors=(size+sector_size-1)//sector_size;start_sector=offset//sector_size;head_sectors=sectors_per_block-start_sector%sectors_per_block
        if num_sectors<head_sectors:head_sectors=num_sectors
        if num_sectors<2*head_sectors:return(num_sectors+1)//2*sector_size
        else:return(num_sectors-head_sectors)*sector_size
    def override_vddsdio(self,new_voltage):raise NotImplementedInROMError('Overriding VDDSDIO setting only applies to ESP32')
class ESP8266StubLoader(ESP8266ROM):
    ' Access class for ESP8266 stub loader, runs on top of ROM.\n    ';FLASH_WRITE_SIZE=16384;IS_STUB=_C
    def __init__(self,rom_loader):self.secure_download_mode=rom_loader.secure_download_mode;self._port=rom_loader._port;self._trace_enabled=rom_loader._trace_enabled;self.flush_input()
    def get_erase_size(self,offset,size):return size
ESP8266ROM.STUB_CLASS=ESP8266StubLoader
class ESP32ROM(ESPLoader):
    'Access class for ESP32 ROM bootloader\n\n    ';CHIP_NAME=_AG;IMAGE_CHIP_ID=0;IS_STUB=_B;CHIP_DETECT_MAGIC_VALUE=[15736195];IROM_MAP_START=1074593792;IROM_MAP_END=1077936128;DROM_MAP_START=1061158912;DROM_MAP_END=1065353216;STATUS_BYTES_LENGTH=4;SPI_REG_BASE=1072963584;SPI_USR_OFFS=28;SPI_USR1_OFFS=32;SPI_USR2_OFFS=36;SPI_MOSI_DLEN_OFFS=40;SPI_MISO_DLEN_OFFS=44;EFUSE_RD_REG_BASE=1073061888;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT_REG=EFUSE_RD_REG_BASE+24;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT=1<<7;DR_REG_SYSCON_BASE=1073111040;SPI_W0_OFFS=128;UART_CLKDIV_REG=1072955412;XTAL_CLK_DIVIDER=1;FLASH_SIZES={_R:0,_c:16,_S:32,_r:48,_s:64};BOOTLOADER_FLASH_OFFSET=4096;OVERRIDE_VDDSDIO_CHOICES=['1.8V','1.9V','OFF'];MEMORY_MAP=[[0,65536,_g],[1061158912,1065353216,_h],[1065353216,1069547520,_w],[1073217536,1073225728,_i],[1073283072,1073741824,_j],[1073405952,1073741824,_T],[1073610752,1073741820,'DIRAM_DRAM'],[1073741824,1074200576,_N],[1074200576,1074233344,'CACHE_PRO'],[1074233344,1074266112,'CACHE_APP'],[1074266112,1074397184,_U],[1074397184,1074528252,'DIRAM_IRAM'],[1074528256,1074536448,_k],[1074593792,1077936128,_N],[1342177280,1342185472,_x]];FLASH_ENCRYPTED_WRITE_ALIGN=32;' Try to read the BLOCK1 (encryption key) and check if it is valid '
    def is_flash_encryption_key_valid(self):
        ' Bit 0 of efuse_rd_disable[3:0] is mapped to BLOCK1\n        this bit is at position 16 in EFUSE_BLK0_RDATA0_REG ';word0=self.read_efuse(0);rd_disable=word0>>16&1
        if rd_disable:return _C
        else:
            key_word=[0]*7
            for i in range(len(key_word)):
                key_word[i]=self.read_efuse(14+i)
                if key_word[i]!=0:return _C
            return _B
    def get_flash_crypt_config(self):
        ' For flash encryption related commands we need to make sure\n        user has programmed all the relevant efuse correctly so before\n        writing encrypted write_flash_encrypt esptool will verify the values\n        of flash_crypt_config to be non zero if they are not read\n        protected. If the values are zero a warning will be printed\n\n        bit 3 in efuse_rd_disable[3:0] is mapped to flash_crypt_config\n        this bit is at position 19 in EFUSE_BLK0_RDATA0_REG ';word0=self.read_efuse(0);rd_disable=word0>>19&1
        if rd_disable==0:' we can read the flash_crypt_config efuse value\n            so go & read it (EFUSE_BLK0_RDATA5_REG[31:28]) ';word5=self.read_efuse(5);word5=word5>>28&15;return word5
        else:return 15
    def get_encrypted_download_disabled(self):
        if self.read_reg(self.EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT_REG)&self.EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT:return _C
        else:return _B
    def get_pkg_version(self):word3=self.read_efuse(3);pkg_version=word3>>9&7;pkg_version+=(word3>>2&1)<<3;return pkg_version
    def get_chip_revision(self):
        word3=self.read_efuse(3);word5=self.read_efuse(5);apb_ctl_date=self.read_reg(self.DR_REG_SYSCON_BASE+124);rev_bit0=word3>>15&1;rev_bit1=word5>>20&1;rev_bit2=apb_ctl_date>>31&1
        if rev_bit0:
            if rev_bit1:
                if rev_bit2:return 3
                else:return 2
            else:return 1
        return 0
    def get_chip_description(self):
        A='ESP32-D0WD';pkg_version=self.get_pkg_version();chip_revision=self.get_chip_revision();rev3=chip_revision==3;single_core=self.read_efuse(3)&1<<0;chip_name={0:'ESP32-S0WDQ6'if single_core else'ESP32-D0WDQ6',1:'ESP32-S0WD'if single_core else A,2:'ESP32-D2WD',4:'ESP32-U4WDH',5:'ESP32-PICO-V3'if rev3 else'ESP32-PICO-D4',6:'ESP32-PICO-V3-02'}.get(pkg_version,'unknown ESP32')
        if chip_name.startswith(A)and rev3:chip_name+='-V3'
        return _y%(chip_name,chip_revision)
    def get_chip_features(self):
        features=[_f];word3=self.read_efuse(3);chip_ver_dis_bt=word3&1<<1
        if chip_ver_dis_bt==0:features+=['BT']
        chip_ver_dis_app_cpu=word3&1<<0
        if chip_ver_dis_app_cpu:features+=['Single Core']
        else:features+=['Dual Core']
        chip_cpu_freq_rated=word3&1<<13
        if chip_cpu_freq_rated:
            chip_cpu_freq_low=word3&1<<12
            if chip_cpu_freq_low:features+=['160MHz']
            else:features+=['240MHz']
        pkg_version=self.get_pkg_version()
        if pkg_version in[2,4,5,6]:features+=[_AF]
        if pkg_version==6:features+=['Embedded PSRAM']
        word4=self.read_efuse(4);adc_vref=word4>>8&31
        if adc_vref:features+=['VRef calibration in efuse']
        blk3_part_res=word3>>14&1
        if blk3_part_res:features+=['BLK3 partially reserved']
        word6=self.read_efuse(6);coding_scheme=word6&3;features+=['Coding Scheme %s'%{0:'None',1:'3/4',2:'Repeat (UNSUPPORTED)',3:'Invalid'}[coding_scheme]];return features
    def read_efuse(self,n):' Read the nth word of the ESP3x EFUSE region. ';return self.read_reg(self.EFUSE_RD_REG_BASE+4*n)
    def chip_id(self):raise NotSupportedError(self,_AH)
    def read_mac(self):
        ' Read MAC from EFUSE region ';words=[self.read_efuse(2),self.read_efuse(1)];bitstring=struct.pack(_l,*words);bitstring=bitstring[2:8]
        try:return tuple((ord(b)for b in bitstring))
        except TypeError:return tuple(bitstring)
    def get_erase_size(self,offset,size):return size
    def override_vddsdio(self,new_voltage):
        new_voltage=new_voltage.upper()
        if new_voltage not in self.OVERRIDE_VDDSDIO_CHOICES:raise FatalError("The only accepted VDDSDIO overrides are '1.8V', '1.9V' and 'OFF'")
        RTC_CNTL_SDIO_CONF_REG=1072988276;RTC_CNTL_XPD_SDIO_REG=1<<31;RTC_CNTL_DREFH_SDIO_M=3<<29;RTC_CNTL_DREFM_SDIO_M=3<<27;RTC_CNTL_DREFL_SDIO_M=3<<25;RTC_CNTL_SDIO_FORCE=1<<22;RTC_CNTL_SDIO_PD_EN=1<<21;reg_val=RTC_CNTL_SDIO_FORCE;reg_val|=RTC_CNTL_SDIO_PD_EN
        if new_voltage!='OFF':reg_val|=RTC_CNTL_XPD_SDIO_REG
        if new_voltage=='1.9V':reg_val|=RTC_CNTL_DREFH_SDIO_M|RTC_CNTL_DREFM_SDIO_M|RTC_CNTL_DREFL_SDIO_M
        self.write_reg(RTC_CNTL_SDIO_CONF_REG,reg_val);print('VDDSDIO regulator set to %s'%new_voltage)
    def read_flash_slow(self,offset,length,progress_fn):
        BLOCK_LEN=64;data=_F
        while len(data)<length:
            block_len=min(BLOCK_LEN,length-len(data));r=self.check_command('read flash block',self.ESP_READ_FLASH_SLOW,struct.pack(_M,offset+len(data),block_len))
            if len(r)<block_len:raise FatalError('Expected %d byte block, got %d bytes. Serial errors?'%(block_len,len(r)))
            data+=r[:block_len]
            if progress_fn and(len(data)%1024==0 or len(data)==length):progress_fn(len(data),length)
        return data
class ESP32S2ROM(ESP32ROM):
    CHIP_NAME=_AI;IMAGE_CHIP_ID=2;IROM_MAP_START=1074266112;IROM_MAP_END=1085800448;DROM_MAP_START=1056964608;DROM_MAP_END=1061093376;CHIP_DETECT_MAGIC_VALUE=[1990];SPI_REG_BASE=1061167104;SPI_USR_OFFS=24;SPI_USR1_OFFS=28;SPI_USR2_OFFS=32;SPI_MOSI_DLEN_OFFS=36;SPI_MISO_DLEN_OFFS=40;SPI_W0_OFFS=88;MAC_EFUSE_REG=1061265476;UART_CLKDIV_REG=1061158932;FLASH_ENCRYPTED_WRITE_ALIGN=16;EFUSE_BASE=1061265408;EFUSE_RD_REG_BASE=EFUSE_BASE+48;EFUSE_PURPOSE_KEY0_REG=EFUSE_BASE+52;EFUSE_PURPOSE_KEY0_SHIFT=24;EFUSE_PURPOSE_KEY1_REG=EFUSE_BASE+52;EFUSE_PURPOSE_KEY1_SHIFT=28;EFUSE_PURPOSE_KEY2_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY2_SHIFT=0;EFUSE_PURPOSE_KEY3_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY3_SHIFT=4;EFUSE_PURPOSE_KEY4_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY4_SHIFT=8;EFUSE_PURPOSE_KEY5_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY5_SHIFT=12;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT_REG=EFUSE_RD_REG_BASE;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT=1<<19;PURPOSE_VAL_XTS_AES256_KEY_1=2;PURPOSE_VAL_XTS_AES256_KEY_2=3;PURPOSE_VAL_XTS_AES128_KEY=4;UARTDEV_BUF_NO=1073741076;UARTDEV_BUF_NO_USB=2;USB_RAM_BLOCK=2048;GPIO_STRAP_REG=1061175352;GPIO_STRAP_SPI_BOOT_MASK=8;RTC_CNTL_OPTION1_REG=1061191976;RTC_CNTL_FORCE_DOWNLOAD_BOOT_MASK=1;MEMORY_MAP=[[0,65536,_g],[1056964608,1073217536,_h],[1062207488,1073217536,_w],[1073340416,1073348608,_i],[1073340416,1073741824,_j],[1073340416,1074208768,_AJ],[1073414144,1073741824,_T],[1073741824,1073848576,_z],[1073872896,1074200576,_U],[1074200576,1074208768,_k],[1074266112,1082130432,_N],[1342177280,1342185472,_x]]
    def get_pkg_version(self):num_word=3;block1_addr=self.EFUSE_BASE+68;word3=self.read_reg(block1_addr+4*num_word);pkg_version=word3>>21&15;return pkg_version
    def get_chip_description(self):chip_name={0:_AI,1:'ESP32-S2FH16',2:'ESP32-S2FH32'}.get(self.get_pkg_version(),'unknown ESP32-S2');return'%s'%chip_name
    def get_chip_features(self):
        features=[_f]
        if self.secure_download_mode:features+=['Secure Download Mode Enabled']
        pkg_version=self.get_pkg_version()
        if pkg_version in[1,2]:
            if pkg_version==1:features+=['Embedded 2MB Flash']
            elif pkg_version==2:features+=['Embedded 4MB Flash']
            features+=['105C temp rating']
        num_word=4;block2_addr=self.EFUSE_BASE+92;word4=self.read_reg(block2_addr+4*num_word);block2_version=word4>>4&7
        if block2_version==1:features+=['ADC and temperature sensor calibration in BLK2 of efuse']
        return features
    def get_crystal_freq(self):return 40
    def override_vddsdio(self,new_voltage):raise NotImplementedInROMError('VDD_SDIO overrides are not supported for ESP32-S2')
    def read_mac(self):
        mac0=self.read_reg(self.MAC_EFUSE_REG);mac1=self.read_reg(self.MAC_EFUSE_REG+4);bitstring=struct.pack(_l,mac1,mac0)[2:]
        try:return tuple((ord(b)for b in bitstring))
        except TypeError:return tuple(bitstring)
    def get_flash_crypt_config(self):return _A
    def get_key_block_purpose(self,key_block):
        if key_block<0 or key_block>5:raise FatalError(_A0)
        reg,shift=[(self.EFUSE_PURPOSE_KEY0_REG,self.EFUSE_PURPOSE_KEY0_SHIFT),(self.EFUSE_PURPOSE_KEY1_REG,self.EFUSE_PURPOSE_KEY1_SHIFT),(self.EFUSE_PURPOSE_KEY2_REG,self.EFUSE_PURPOSE_KEY2_SHIFT),(self.EFUSE_PURPOSE_KEY3_REG,self.EFUSE_PURPOSE_KEY3_SHIFT),(self.EFUSE_PURPOSE_KEY4_REG,self.EFUSE_PURPOSE_KEY4_SHIFT),(self.EFUSE_PURPOSE_KEY5_REG,self.EFUSE_PURPOSE_KEY5_SHIFT)][key_block];return self.read_reg(reg)>>shift&15
    def is_flash_encryption_key_valid(self):
        purposes=[self.get_key_block_purpose(b)for b in range(6)]
        if any((p==self.PURPOSE_VAL_XTS_AES128_KEY for p in purposes)):return _C
        return any((p==self.PURPOSE_VAL_XTS_AES256_KEY_1 for p in purposes))and any((p==self.PURPOSE_VAL_XTS_AES256_KEY_2 for p in purposes))
    def uses_usb(self,_cache=[]):
        if self.secure_download_mode:return _B
        if not _cache:buf_no=self.read_reg(self.UARTDEV_BUF_NO)&255;_cache.append(buf_no==self.UARTDEV_BUF_NO_USB)
        return _cache[0]
    def _post_connect(self):
        if self.uses_usb():self.ESP_RAM_BLOCK=self.USB_RAM_BLOCK
    def _check_if_can_reset(self):
        '\n        Check the strapping register to see if we can reset out of download mode.\n        '
        if os.getenv('ESPTOOL_TESTING')is not _A:print('ESPTOOL_TESTING is set, ignoring strapping mode check');return
        strap_reg=self.read_reg(self.GPIO_STRAP_REG);force_dl_reg=self.read_reg(self.RTC_CNTL_OPTION1_REG)
        if strap_reg&self.GPIO_STRAP_SPI_BOOT_MASK==0 and force_dl_reg&self.RTC_CNTL_FORCE_DOWNLOAD_BOOT_MASK==0:print("ERROR: {} chip was placed into download mode using GPIO0.\nesptool.py can not exit the download mode over USB. To run the app, reset the chip manually.\nTo suppress this error, set --after option to 'no_reset'.".format(self.get_chip_description()));raise SystemExit(1)
    def hard_reset(self):
        if self.uses_usb():self._check_if_can_reset()
        self._setRTS(_C)
        if self.uses_usb():time.sleep(0.2);self._setRTS(_B);time.sleep(0.2)
        else:self._setRTS(_B)
class ESP32S3ROM(ESP32ROM):
    CHIP_NAME=_AK;IROM_MAP_START=1107296256;IROM_MAP_END=1140850688;DROM_MAP_START=1006632960;DROM_MAP_END=1040187392;UART_DATE_REG_ADDR=1610612864;SPI_REG_BASE=1610620928;SPI_USR_OFFS=24;SPI_USR1_OFFS=28;SPI_USR2_OFFS=32;SPI_MOSI_DLEN_OFFS=36;SPI_MISO_DLEN_OFFS=40;SPI_W0_OFFS=88;FLASH_ENCRYPTED_WRITE_ALIGN=16;EFUSE_BASE=1610719232;MAC_EFUSE_REG=EFUSE_BASE+68;EFUSE_RD_REG_BASE=EFUSE_BASE+48;EFUSE_PURPOSE_KEY0_REG=EFUSE_BASE+52;EFUSE_PURPOSE_KEY0_SHIFT=24;EFUSE_PURPOSE_KEY1_REG=EFUSE_BASE+52;EFUSE_PURPOSE_KEY1_SHIFT=28;EFUSE_PURPOSE_KEY2_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY2_SHIFT=0;EFUSE_PURPOSE_KEY3_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY3_SHIFT=4;EFUSE_PURPOSE_KEY4_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY4_SHIFT=8;EFUSE_PURPOSE_KEY5_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY5_SHIFT=12;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT_REG=EFUSE_RD_REG_BASE;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT=1<<20;PURPOSE_VAL_XTS_AES256_KEY_1=2;PURPOSE_VAL_XTS_AES256_KEY_2=3;PURPOSE_VAL_XTS_AES128_KEY=4;UART_CLKDIV_REG=1610612756;GPIO_STRAP_REG=1610629176;MEMORY_MAP=[[0,65536,_g],[1006632960,1023410176,_h],[1023410176,1040187392,_w],[1611653120,1611661312,_i],[1070104576,1070596096,_j],[1070104576,1077813248,_AJ],[1070104576,1070596096,_T],[1073741824,1073848576,_z],[1077346304,1077805056,_U],[1611653120,1611661312,_k],[1107296256,1115684864,_N],[1342177280,1342185472,_x]]
    def get_chip_description(self):return _AK
    def get_chip_features(self):return[_f,'BLE']
    def get_crystal_freq(self):return 40
    def get_flash_crypt_config(self):return _A
    def get_key_block_purpose(self,key_block):
        if key_block<0 or key_block>5:raise FatalError(_A0)
        reg,shift=[(self.EFUSE_PURPOSE_KEY0_REG,self.EFUSE_PURPOSE_KEY0_SHIFT),(self.EFUSE_PURPOSE_KEY1_REG,self.EFUSE_PURPOSE_KEY1_SHIFT),(self.EFUSE_PURPOSE_KEY2_REG,self.EFUSE_PURPOSE_KEY2_SHIFT),(self.EFUSE_PURPOSE_KEY3_REG,self.EFUSE_PURPOSE_KEY3_SHIFT),(self.EFUSE_PURPOSE_KEY4_REG,self.EFUSE_PURPOSE_KEY4_SHIFT),(self.EFUSE_PURPOSE_KEY5_REG,self.EFUSE_PURPOSE_KEY5_SHIFT)][key_block];return self.read_reg(reg)>>shift&15
    def is_flash_encryption_key_valid(self):
        purposes=[self.get_key_block_purpose(b)for b in range(6)]
        if any((p==self.PURPOSE_VAL_XTS_AES128_KEY for p in purposes)):return _C
        return any((p==self.PURPOSE_VAL_XTS_AES256_KEY_1 for p in purposes))and any((p==self.PURPOSE_VAL_XTS_AES256_KEY_2 for p in purposes))
    def override_vddsdio(self,new_voltage):raise NotImplementedInROMError('VDD_SDIO overrides are not supported for ESP32-S3')
    def read_mac(self):
        mac0=self.read_reg(self.MAC_EFUSE_REG);mac1=self.read_reg(self.MAC_EFUSE_REG+4);bitstring=struct.pack(_l,mac1,mac0)[2:]
        try:return tuple((ord(b)for b in bitstring))
        except TypeError:return tuple(bitstring)
class ESP32S3BETA2ROM(ESP32S3ROM):
    CHIP_NAME=_AL;IMAGE_CHIP_ID=4;CHIP_DETECT_MAGIC_VALUE=[3942662454]
    def get_chip_description(self):return _AL
class ESP32S3BETA3ROM(ESP32S3ROM):
    CHIP_NAME=_AM;IMAGE_CHIP_ID=6;CHIP_DETECT_MAGIC_VALUE=[9]
    def get_chip_description(self):return _AM
class ESP32C3ROM(ESP32ROM):
    CHIP_NAME=_AN;IMAGE_CHIP_ID=5;IROM_MAP_START=1107296256;IROM_MAP_END=1115684864;DROM_MAP_START=1006632960;DROM_MAP_END=1015021568;SPI_REG_BASE=1610620928;SPI_USR_OFFS=24;SPI_USR1_OFFS=28;SPI_USR2_OFFS=32;SPI_MOSI_DLEN_OFFS=36;SPI_MISO_DLEN_OFFS=40;SPI_W0_OFFS=88;BOOTLOADER_FLASH_OFFSET=0;CHIP_DETECT_MAGIC_VALUE=[1763790959,456216687];UART_DATE_REG_ADDR=1610612736+124;EFUSE_BASE=1610647552;MAC_EFUSE_REG=EFUSE_BASE+68;EFUSE_RD_REG_BASE=EFUSE_BASE+48;EFUSE_PURPOSE_KEY0_REG=EFUSE_BASE+52;EFUSE_PURPOSE_KEY0_SHIFT=24;EFUSE_PURPOSE_KEY1_REG=EFUSE_BASE+52;EFUSE_PURPOSE_KEY1_SHIFT=28;EFUSE_PURPOSE_KEY2_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY2_SHIFT=0;EFUSE_PURPOSE_KEY3_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY3_SHIFT=4;EFUSE_PURPOSE_KEY4_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY4_SHIFT=8;EFUSE_PURPOSE_KEY5_REG=EFUSE_BASE+56;EFUSE_PURPOSE_KEY5_SHIFT=12;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT_REG=EFUSE_RD_REG_BASE;EFUSE_DIS_DOWNLOAD_MANUAL_ENCRYPT=1<<20;PURPOSE_VAL_XTS_AES128_KEY=4;GPIO_STRAP_REG=1061175352;FLASH_ENCRYPTED_WRITE_ALIGN=16;MEMORY_MAP=[[0,65536,_g],[1006632960,1015021568,_h],[1070071808,1070465024,_T],[1070104576,1070596096,_j],[1072693248,1072824320,'DROM_MASK'],[1073741824,1074135040,_z],[1107296256,1115684864,_N],[1077395456,1077805056,_U],[1342177280,1342185472,_k],[1342177280,1342185472,_i],[1611653120,1611661312,'MEM_INTERNAL2']]
    def get_pkg_version(self):num_word=3;block1_addr=self.EFUSE_BASE+68;word3=self.read_reg(block1_addr+4*num_word);pkg_version=word3>>21&15;return pkg_version
    def get_chip_revision(self):block1_addr=self.EFUSE_BASE+68;num_word=3;pos=18;return(self.read_reg(block1_addr+4*num_word)&7<<pos)>>pos
    def get_chip_description(self):chip_name={0:_AN}.get(self.get_pkg_version(),'unknown ESP32-C3');chip_revision=self.get_chip_revision();return _y%(chip_name,chip_revision)
    def get_chip_features(self):return['Wi-Fi']
    def get_crystal_freq(self):return 40
    def override_vddsdio(self,new_voltage):raise NotImplementedInROMError('VDD_SDIO overrides are not supported for ESP32-C3')
    def read_mac(self):
        mac0=self.read_reg(self.MAC_EFUSE_REG);mac1=self.read_reg(self.MAC_EFUSE_REG+4);bitstring=struct.pack(_l,mac1,mac0)[2:]
        try:return tuple((ord(b)for b in bitstring))
        except TypeError:return tuple(bitstring)
    def get_flash_crypt_config(self):return _A
    def get_key_block_purpose(self,key_block):
        if key_block<0 or key_block>5:raise FatalError(_A0)
        reg,shift=[(self.EFUSE_PURPOSE_KEY0_REG,self.EFUSE_PURPOSE_KEY0_SHIFT),(self.EFUSE_PURPOSE_KEY1_REG,self.EFUSE_PURPOSE_KEY1_SHIFT),(self.EFUSE_PURPOSE_KEY2_REG,self.EFUSE_PURPOSE_KEY2_SHIFT),(self.EFUSE_PURPOSE_KEY3_REG,self.EFUSE_PURPOSE_KEY3_SHIFT),(self.EFUSE_PURPOSE_KEY4_REG,self.EFUSE_PURPOSE_KEY4_SHIFT),(self.EFUSE_PURPOSE_KEY5_REG,self.EFUSE_PURPOSE_KEY5_SHIFT)][key_block];return self.read_reg(reg)>>shift&15
    def is_flash_encryption_key_valid(self):purposes=[self.get_key_block_purpose(b)for b in range(6)];return any((p==self.PURPOSE_VAL_XTS_AES128_KEY for p in purposes))
class ESP32C6BETAROM(ESP32C3ROM):
    CHIP_NAME='ESP32-C6 BETA';IMAGE_CHIP_ID=7;CHIP_DETECT_MAGIC_VALUE=[228687983];UART_DATE_REG_ADDR=1280
    def get_chip_description(self):chip_name={0:'ESP32-C6'}.get(self.get_pkg_version(),'unknown ESP32-C6');chip_revision=self.get_chip_revision();return _y%(chip_name,chip_revision)
class ESP32StubLoader(ESP32ROM):
    ' Access class for ESP32 stub loader, runs on top of ROM.\n    ';FLASH_WRITE_SIZE=16384;STATUS_BYTES_LENGTH=2;IS_STUB=_C
    def __init__(self,rom_loader):self.secure_download_mode=rom_loader.secure_download_mode;self._port=rom_loader._port;self._trace_enabled=rom_loader._trace_enabled;self.flush_input()
ESP32ROM.STUB_CLASS=ESP32StubLoader
class ESP32S2StubLoader(ESP32S2ROM):
    ' Access class for ESP32-S2 stub loader, runs on top of ROM.\n\n    (Basically the same as ESP32StubLoader, but different base class.\n    Can possibly be made into a mixin.)\n    ';FLASH_WRITE_SIZE=16384;STATUS_BYTES_LENGTH=2;IS_STUB=_C
    def __init__(self,rom_loader):
        self.secure_download_mode=rom_loader.secure_download_mode;self._port=rom_loader._port;self._trace_enabled=rom_loader._trace_enabled;self.flush_input()
        if rom_loader.uses_usb():self.ESP_RAM_BLOCK=self.USB_RAM_BLOCK;self.FLASH_WRITE_SIZE=self.USB_RAM_BLOCK
ESP32S2ROM.STUB_CLASS=ESP32S2StubLoader
class ESP32S3BETA2StubLoader(ESP32S3BETA2ROM):
    ' Access class for ESP32S3 stub loader, runs on top of ROM.\n\n    (Basically the same as ESP32StubLoader, but different base class.\n    Can possibly be made into a mixin.)\n    ';FLASH_WRITE_SIZE=16384;STATUS_BYTES_LENGTH=2;IS_STUB=_C
    def __init__(self,rom_loader):self.secure_download_mode=rom_loader.secure_download_mode;self._port=rom_loader._port;self._trace_enabled=rom_loader._trace_enabled;self.flush_input()
ESP32S3BETA2ROM.STUB_CLASS=ESP32S3BETA2StubLoader
class ESP32S3BETA3StubLoader(ESP32S3BETA3ROM):
    ' Access class for ESP32S3 stub loader, runs on top of ROM.\n\n    (Basically the same as ESP32StubLoader, but different base class.\n    Can possibly be made into a mixin.)\n    ';FLASH_WRITE_SIZE=16384;STATUS_BYTES_LENGTH=2;IS_STUB=_C
    def __init__(self,rom_loader):self.secure_download_mode=rom_loader.secure_download_mode;self._port=rom_loader._port;self._trace_enabled=rom_loader._trace_enabled;self.flush_input()
ESP32S3BETA3ROM.STUB_CLASS=ESP32S3BETA3StubLoader
class ESP32C3StubLoader(ESP32C3ROM):
    ' Access class for ESP32C3 stub loader, runs on top of ROM.\n\n    (Basically the same as ESP32StubLoader, but different base class.\n    Can possibly be made into a mixin.)\n    ';FLASH_WRITE_SIZE=16384;STATUS_BYTES_LENGTH=2;IS_STUB=_C
    def __init__(self,rom_loader):self.secure_download_mode=rom_loader.secure_download_mode;self._port=rom_loader._port;self._trace_enabled=rom_loader._trace_enabled;self.flush_input()
ESP32C3ROM.STUB_CLASS=ESP32C3StubLoader
class ESPBOOTLOADER:" These are constants related to software ESP8266 bootloader, working with 'v2' image files ";IMAGE_V2_MAGIC=234;IMAGE_V2_SEGMENT=4
def LoadFirmwareImage(chip,filename):
    ' Load a firmware image. Can be for any supported SoC.\n\n        ESP8266 images will be examined to determine if they are original ROM firmware images (ESP8266ROMFirmwareImage)\n        or "v2" OTA bootloader images.\n\n        Returns a BaseFirmwareImage subclass, either ESP8266ROMFirmwareImage (v1) or ESP8266V2FirmwareImage (v2).\n    ';chip=chip.lower().replace('-','')
    with open(filename,_O)as f:
        if chip==_W:return ESP32FirmwareImage(f)
        elif chip==_X:return ESP32S2FirmwareImage(f)
        elif chip==_Y:return ESP32S3BETA2FirmwareImage(f)
        elif chip==_Z:return ESP32S3BETA3FirmwareImage(f)
        elif chip==_a:return ESP32C3FirmwareImage(f)
        elif chip==_b:return ESP32C6BETAFirmwareImage(f)
        else:
            magic=ord(f.read(1));f.seek(0)
            if magic==ESPLoader.ESP_IMAGE_MAGIC:return ESP8266ROMFirmwareImage(f)
            elif magic==ESPBOOTLOADER.IMAGE_V2_MAGIC:return ESP8266V2FirmwareImage(f)
            else:raise FatalError('Invalid image magic number: %d'%magic)
class ImageSegment:
    ' Wrapper class for a segment in an ESP image\n    (very similar to a section in an ELFImage also) '
    def __init__(self,addr,data,file_offs=_A):
        self.addr=addr;self.data=data;self.file_offs=file_offs;self.include_in_checksum=_C
        if self.addr!=0:self.pad_to_alignment(4)
    def copy_with_new_addr(self,new_addr):' Return a new ImageSegment with same data, but mapped at\n        a new address. ';return ImageSegment(new_addr,self.data,0)
    def split_image(self,split_len):' Return a new ImageSegment which splits "split_len" bytes\n        from the beginning of the data. Remaining bytes are kept in\n        this segment object (and the start address is adjusted to match.) ';result=copy.copy(self);result.data=self.data[:split_len];self.data=self.data[split_len:];self.addr+=split_len;self.file_offs=_A;result.file_offs=_A;return result
    def __repr__(self):
        r='len 0x%05x load 0x%08x'%(len(self.data),self.addr)
        if self.file_offs is not _A:r+=' file_offs 0x%08x'%self.file_offs
        return r
    def get_memory_type(self,image):"\n        Return a list describing the memory type(s) that is covered by this\n        segment's start address.\n        ";return[map_range[2]for map_range in image.ROM_LOADER.MEMORY_MAP if map_range[0]<=self.addr<map_range[1]]
    def pad_to_alignment(self,alignment):self.data=pad_to(self.data,alignment,_I)
class ELFSection(ImageSegment):
    ' Wrapper class for a section in an ELF image, has a section\n    name as well as the common properties of an ImageSegment. '
    def __init__(self,name,addr,data):super(ELFSection,self).__init__(addr,data);self.name=name.decode(_AB)
    def __repr__(self):return'%s %s'%(self.name,super(ELFSection,self).__repr__())
class BaseFirmwareImage:
    SEG_HEADER_LEN=8;SHA256_DIGEST_LEN=32;' Base class with common firmware image functions '
    def __init__(self):self.segments=[];self.entrypoint=0;self.elf_sha256=_A;self.elf_sha256_offset=0
    def load_common_header(self,load_file,expected_magic):
        magic,segments,self.flash_mode,self.flash_size_freq,self.entrypoint=struct.unpack(_AO,load_file.read(8))
        if magic!=expected_magic:raise FatalError('Invalid firmware image magic=0x%x'%magic)
        return segments
    def verify(self):
        if len(self.segments)>16:raise FatalError('Invalid segment count %d (max 16). Usually this indicates a linker script problem.'%len(self.segments))
    def load_segment(self,f,is_irom_segment=_B):
        ' Load the next segment from the image file ';file_offs=f.tell();offset,size=struct.unpack(_M,f.read(8));self.warn_if_unusual_segment(offset,size,is_irom_segment);segment_data=f.read(size)
        if len(segment_data)<size:raise FatalError('End of file reading segment 0x%x, length %d (actual length %d)'%(offset,size,len(segment_data)))
        segment=ImageSegment(offset,segment_data,file_offs);self.segments.append(segment);return segment
    def warn_if_unusual_segment(self,offset,size,is_irom_segment):
        if not is_irom_segment:
            if offset>1075838976 or offset<1073610752 or size>65536:print('WARNING: Suspicious segment 0x%x, length %d'%(offset,size))
    def maybe_patch_segment_data(self,f,segment_data):
        'If SHA256 digest of the ELF file needs to be inserted into this segment, do so. Returns segment data.';segment_len=len(segment_data);file_pos=f.tell()
        if self.elf_sha256_offset>=file_pos and self.elf_sha256_offset<file_pos+segment_len:
            patch_offset=self.elf_sha256_offset-file_pos
            if patch_offset<self.SEG_HEADER_LEN or patch_offset+self.SHA256_DIGEST_LEN>segment_len:raise FatalError('Cannot place SHA256 digest on segment boundary(elf_sha256_offset=%d, file_pos=%d, segment_size=%d)'%(self.elf_sha256_offset,file_pos,segment_len))
            patch_offset-=self.SEG_HEADER_LEN
            if segment_data[patch_offset:patch_offset+self.SHA256_DIGEST_LEN]!=_I*self.SHA256_DIGEST_LEN:raise FatalError('Contents of segment at SHA256 digest offset 0x%x are not all zero. Refusing to overwrite.'%self.elf_sha256_offset)
            assert len(self.elf_sha256)==self.SHA256_DIGEST_LEN;segment_data=segment_data[0:patch_offset]+self.elf_sha256+segment_data[patch_offset+self.SHA256_DIGEST_LEN:]
        return segment_data
    def save_segment(self,f,segment,checksum=_A):
        ' Save the next segment to the image file, return next checksum value if provided ';segment_data=self.maybe_patch_segment_data(f,segment.data);f.write(struct.pack(_M,segment.addr,len(segment_data)));f.write(segment_data)
        if checksum is not _A:return ESPLoader.checksum(segment_data,checksum)
    def read_checksum(self,f):' Return ESPLoader checksum from end of just-read image ';align_file_position(f,16);return ord(f.read(1))
    def calculate_checksum(self):
        ' Calculate checksum of loaded image, based on segments in\n        segment array.\n        ';checksum=ESPLoader.ESP_CHECKSUM_MAGIC
        for seg in self.segments:
            if seg.include_in_checksum:checksum=ESPLoader.checksum(seg.data,checksum)
        return checksum
    def append_checksum(self,f,checksum):' Append ESPLoader checksum to the just-written image ';align_file_position(f,16);f.write(struct.pack(b'B',checksum))
    def write_common_header(self,f,segments):f.write(struct.pack(_AO,ESPLoader.ESP_IMAGE_MAGIC,len(segments),self.flash_mode,self.flash_size_freq,self.entrypoint))
    def is_irom_addr(self,addr):' Returns True if an address starts in the irom region.\n        Valid for ESP8266 only.\n        ';return ESP8266ROM.IROM_MAP_START<=addr<ESP8266ROM.IROM_MAP_END
    def get_irom_segment(self):
        irom_segments=[s for s in self.segments if self.is_irom_addr(s.addr)]
        if len(irom_segments)>0:
            if len(irom_segments)!=1:raise FatalError('Found %d segments that could be irom0. Bad ELF file?'%len(irom_segments))
            return irom_segments[0]
        return _A
    def get_non_irom_segments(self):irom_segment=self.get_irom_segment();return[s for s in self.segments if s!=irom_segment]
    def merge_adjacent_segments(self):
        if not self.segments:return
        segments=[]
        for i in range(len(self.segments)-1,0,-1):
            elem=self.segments[i-1];next_elem=self.segments[i]
            if all((elem.get_memory_type(self)==next_elem.get_memory_type(self),elem.include_in_checksum==next_elem.include_in_checksum,next_elem.addr==elem.addr+len(elem.data))):elem.data+=next_elem.data
            else:segments.insert(0,next_elem)
        segments.insert(0,self.segments[0]);self.segments=segments
class ESP8266ROMFirmwareImage(BaseFirmwareImage):
    " 'Version 1' firmware image, segments loaded directly by the ROM bootloader. ";ROM_LOADER=ESP8266ROM
    def __init__(self,load_file=_A):
        super(ESP8266ROMFirmwareImage,self).__init__();self.flash_mode=0;self.flash_size_freq=0;self.version=1
        if load_file is not _A:
            segments=self.load_common_header(load_file,ESPLoader.ESP_IMAGE_MAGIC)
            for _ in range(segments):self.load_segment(load_file)
            self.checksum=self.read_checksum(load_file);self.verify()
    def default_output_name(self,input_file):' Derive a default output name from the ELF name. ';return input_file+'-'
    def save(self,basename):
        ' Save a set of V1 images for flashing. Parameter is a base filename. ';irom_segment=self.get_irom_segment()
        if irom_segment is not _A:
            with open('%s0x%05x.bin'%(basename,irom_segment.addr-ESP8266ROM.IROM_MAP_START),_J)as f:f.write(irom_segment.data)
        normal_segments=self.get_non_irom_segments()
        with open('%s0x00000.bin'%basename,_J)as f:
            self.write_common_header(f,normal_segments);checksum=ESPLoader.ESP_CHECKSUM_MAGIC
            for segment in normal_segments:checksum=self.save_segment(f,segment,checksum)
            self.append_checksum(f,checksum)
ESP8266ROM.BOOTLOADER_IMAGE=ESP8266ROMFirmwareImage
class ESP8266V2FirmwareImage(BaseFirmwareImage):
    " 'Version 2' firmware image, segments loaded by software bootloader stub\n        (ie Espressif bootloader or rboot)\n    ";ROM_LOADER=ESP8266ROM
    def __init__(self,load_file=_A):
        super(ESP8266V2FirmwareImage,self).__init__();self.version=2
        if load_file is not _A:
            segments=self.load_common_header(load_file,ESPBOOTLOADER.IMAGE_V2_MAGIC)
            if segments!=ESPBOOTLOADER.IMAGE_V2_SEGMENT:print('Warning: V2 header has unexpected "segment" count %d (usually 4)'%segments)
            irom_segment=self.load_segment(load_file,_C);irom_segment.addr=0;irom_segment.include_in_checksum=_B;first_flash_mode=self.flash_mode;first_flash_size_freq=self.flash_size_freq;first_entrypoint=self.entrypoint;segments=self.load_common_header(load_file,ESPLoader.ESP_IMAGE_MAGIC)
            if first_flash_mode!=self.flash_mode:print('WARNING: Flash mode value in first header (0x%02x) disagrees with second (0x%02x). Using second value.'%(first_flash_mode,self.flash_mode))
            if first_flash_size_freq!=self.flash_size_freq:print('WARNING: Flash size/freq value in first header (0x%02x) disagrees with second (0x%02x). Using second value.'%(first_flash_size_freq,self.flash_size_freq))
            if first_entrypoint!=self.entrypoint:print('WARNING: Entrypoint address in first header (0x%08x) disagrees with second header (0x%08x). Using second value.'%(first_entrypoint,self.entrypoint))
            for _ in range(segments):self.load_segment(load_file)
            self.checksum=self.read_checksum(load_file);self.verify()
    def default_output_name(self,input_file):
        ' Derive a default output name from the ELF name. ';irom_segment=self.get_irom_segment()
        if irom_segment is not _A:irom_offs=irom_segment.addr-ESP8266ROM.IROM_MAP_START
        else:irom_offs=0
        return'%s-0x%05x.bin'%(os.path.splitext(input_file)[0],irom_offs&~(ESPLoader.FLASH_SECTOR_SIZE-1))
    def save(self,filename):
        with open(filename,_J)as f:
            f.write(struct.pack(b'<BBBBI',ESPBOOTLOADER.IMAGE_V2_MAGIC,ESPBOOTLOADER.IMAGE_V2_SEGMENT,self.flash_mode,self.flash_size_freq,self.entrypoint));irom_segment=self.get_irom_segment()
            if irom_segment is not _A:irom_segment=irom_segment.copy_with_new_addr(0);irom_segment.pad_to_alignment(16);self.save_segment(f,irom_segment)
            normal_segments=self.get_non_irom_segments();self.write_common_header(f,normal_segments);checksum=ESPLoader.ESP_CHECKSUM_MAGIC
            for segment in normal_segments:checksum=self.save_segment(f,segment,checksum)
            self.append_checksum(f,checksum)
        with open(filename,_O)as f:crc=esp8266_crc32(f.read())
        with open(filename,'ab')as f:f.write(struct.pack(b'<I',crc))
ESPFirmwareImage=ESP8266ROMFirmwareImage
OTAFirmwareImage=ESP8266V2FirmwareImage
def esp8266_crc32(data):
    '\n    CRC32 algorithm used by 8266 SDK bootloader (and gen_appbin.py).\n    ';crc=binascii.crc32(data,0)&4294967295
    if crc&2147483648:return crc^4294967295
    else:return crc+1
class ESP32FirmwareImage(BaseFirmwareImage):
    ' ESP32 firmware image is very similar to V1 ESP8266 image,\n    except with an additional 16 byte reserved header at top of image,\n    and because of new flash mapping capabilities the flash-mapped regions\n    can be placed in the normal image (just @ 64kB padded offsets).\n    ';ROM_LOADER=ESP32ROM;WP_PIN_DISABLED=238;EXTENDED_HEADER_STRUCT_FMT='<BBBBHB'+'B'*8+'B';IROM_ALIGN=65536
    def __init__(self,load_file=_A):
        super(ESP32FirmwareImage,self).__init__();self.secure_pad=_A;self.flash_mode=0;self.flash_size_freq=0;self.version=1;self.wp_pin=self.WP_PIN_DISABLED;self.clk_drv=0;self.q_drv=0;self.d_drv=0;self.cs_drv=0;self.hd_drv=0;self.wp_drv=0;self.min_rev=0;self.append_digest=_C
        if load_file is not _A:
            start=load_file.tell();segments=self.load_common_header(load_file,ESPLoader.ESP_IMAGE_MAGIC);self.load_extended_header(load_file)
            for _ in range(segments):self.load_segment(load_file)
            self.checksum=self.read_checksum(load_file)
            if self.append_digest:end=load_file.tell();self.stored_digest=load_file.read(32);load_file.seek(start);calc_digest=hashlib.sha256();calc_digest.update(load_file.read(end-start));self.calc_digest=calc_digest.digest()
            self.verify()
    def is_flash_addr(self,addr):return self.ROM_LOADER.IROM_MAP_START<=addr<self.ROM_LOADER.IROM_MAP_END or self.ROM_LOADER.DROM_MAP_START<=addr<self.ROM_LOADER.DROM_MAP_END
    def default_output_name(self,input_file):' Derive a default output name from the ELF name. ';return'%s.bin'%os.path.splitext(input_file)[0]
    def warn_if_unusual_segment(self,offset,size,is_irom_segment):0
    def save(self,filename):
        total_segments=0
        with io.BytesIO()as f:
            self.write_common_header(f,self.segments);self.save_extended_header(f);checksum=ESPLoader.ESP_CHECKSUM_MAGIC;flash_segments=[copy.deepcopy(s)for s in sorted(self.segments,key=lambda s:s.addr)if self.is_flash_addr(s.addr)];ram_segments=[copy.deepcopy(s)for s in sorted(self.segments,key=lambda s:s.addr)if not self.is_flash_addr(s.addr)]
            if len(flash_segments)>0:
                last_addr=flash_segments[0].addr
                for segment in flash_segments[1:]:
                    if segment.addr//self.IROM_ALIGN==last_addr//self.IROM_ALIGN:raise FatalError("Segment loaded at 0x%08x lands in same 64KB flash mapping as segment loaded at 0x%08x. Can't generate binary. Suggest changing linker script or ELF to merge sections."%(segment.addr,last_addr))
                    last_addr=segment.addr
            def get_alignment_data_needed(segment):
                align_past=segment.addr%self.IROM_ALIGN-self.SEG_HEADER_LEN;pad_len=self.IROM_ALIGN-f.tell()%self.IROM_ALIGN+align_past
                if pad_len==0 or pad_len==self.IROM_ALIGN:return 0
                pad_len-=self.SEG_HEADER_LEN
                if pad_len<0:pad_len+=self.IROM_ALIGN
                return pad_len
            while len(flash_segments)>0:
                segment=flash_segments[0];pad_len=get_alignment_data_needed(segment)
                if pad_len>0:
                    if len(ram_segments)>0 and pad_len>self.SEG_HEADER_LEN:
                        pad_segment=ram_segments[0].split_image(pad_len)
                        if len(ram_segments[0].data)==0:ram_segments.pop(0)
                    else:pad_segment=ImageSegment(0,_I*pad_len,f.tell())
                    checksum=self.save_segment(f,pad_segment,checksum);total_segments+=1
                else:assert(f.tell()+8)%self.IROM_ALIGN==segment.addr%self.IROM_ALIGN;checksum=self.save_flash_segment(f,segment,checksum);flash_segments.pop(0);total_segments+=1
            for segment in ram_segments:checksum=self.save_segment(f,segment,checksum);total_segments+=1
            if self.secure_pad:
                if not self.append_digest:raise FatalError('secure_pad only applies if a SHA-256 digest is also appended to the image')
                align_past=(f.tell()+self.SEG_HEADER_LEN)%self.IROM_ALIGN;checksum_space=16
                if self.secure_pad==_P:space_after_checksum=32+4+64+12
                elif self.secure_pad==_G:space_after_checksum=32
                pad_len=(self.IROM_ALIGN-align_past-checksum_space-space_after_checksum)%self.IROM_ALIGN;pad_segment=ImageSegment(0,_I*pad_len,f.tell());checksum=self.save_segment(f,pad_segment,checksum);total_segments+=1
            self.append_checksum(f,checksum);image_length=f.tell()
            if self.secure_pad:assert(image_length+space_after_checksum)%self.IROM_ALIGN==0
            f.seek(1)
            try:f.write(chr(total_segments))
            except TypeError:f.write(bytes([total_segments]))
            if self.append_digest:f.seek(0);digest=hashlib.sha256();digest.update(f.read(image_length));f.write(digest.digest())
            with open(filename,_J)as real_file:real_file.write(f.getvalue())
    def save_flash_segment(self,f,segment,checksum=_A):
        ' Save the next segment to the image file, return next checksum value if provided ';segment_end_pos=f.tell()+len(segment.data)+self.SEG_HEADER_LEN;segment_len_remainder=segment_end_pos%self.IROM_ALIGN
        if segment_len_remainder<36:segment.data+=_I*(36-segment_len_remainder)
        return self.save_segment(f,segment,checksum)
    def load_extended_header(self,load_file):
        def split_byte(n):return n&15,n>>4&15
        fields=list(struct.unpack(self.EXTENDED_HEADER_STRUCT_FMT,load_file.read(16)));self.wp_pin=fields[0];self.clk_drv,self.q_drv=split_byte(fields[1]);self.d_drv,self.cs_drv=split_byte(fields[2]);self.hd_drv,self.wp_drv=split_byte(fields[3]);chip_id=fields[4]
        if chip_id!=self.ROM_LOADER.IMAGE_CHIP_ID:print('Unexpected chip id in image. Expected %d but value was %d. Is this image for a different chip model?'%(self.ROM_LOADER.IMAGE_CHIP_ID,chip_id))
        if any((f for f in fields[6:-1]if f!=0)):print('Warning: some reserved header fields have non-zero values. This image may be from a newer esptool.py?')
        append_digest=fields[-1]
        if append_digest in[0,1]:self.append_digest=append_digest==1
        else:raise RuntimeError('Invalid value for append_digest field (0x%02x). Should be 0 or 1.',append_digest)
    def save_extended_header(self,save_file):
        def join_byte(ln,hn):return(ln&15)+((hn&15)<<4)
        append_digest=1 if self.append_digest else 0;fields=[self.wp_pin,join_byte(self.clk_drv,self.q_drv),join_byte(self.d_drv,self.cs_drv),join_byte(self.hd_drv,self.wp_drv),self.ROM_LOADER.IMAGE_CHIP_ID,self.min_rev];fields+=[0]*8;fields+=[append_digest];packed=struct.pack(self.EXTENDED_HEADER_STRUCT_FMT,*fields);save_file.write(packed)
ESP32ROM.BOOTLOADER_IMAGE=ESP32FirmwareImage
class ESP32S2FirmwareImage(ESP32FirmwareImage):' ESP32S2 Firmware Image almost exactly the same as ESP32FirmwareImage ';ROM_LOADER=ESP32S2ROM
ESP32S2ROM.BOOTLOADER_IMAGE=ESP32S2FirmwareImage
class ESP32S3BETA2FirmwareImage(ESP32FirmwareImage):' ESP32S3 Firmware Image almost exactly the same as ESP32FirmwareImage ';ROM_LOADER=ESP32S3BETA2ROM
ESP32S3BETA2ROM.BOOTLOADER_IMAGE=ESP32S3BETA2FirmwareImage
class ESP32S3BETA3FirmwareImage(ESP32FirmwareImage):' ESP32S3 Firmware Image almost exactly the same as ESP32FirmwareImage ';ROM_LOADER=ESP32S3BETA3ROM
ESP32S3BETA3ROM.BOOTLOADER_IMAGE=ESP32S3BETA3FirmwareImage
class ESP32C3FirmwareImage(ESP32FirmwareImage):' ESP32C3 Firmware Image almost exactly the same as ESP32FirmwareImage ';ROM_LOADER=ESP32C3ROM
ESP32C3ROM.BOOTLOADER_IMAGE=ESP32C3FirmwareImage
class ESP32C6BETAFirmwareImage(ESP32FirmwareImage):' ESP32C6 Firmware Image almost exactly the same as ESP32FirmwareImage ';ROM_LOADER=ESP32C6BETAROM
ESP32C6BETAROM.BOOTLOADER_IMAGE=ESP32C6BETAFirmwareImage
class ELFFile:
    SEC_TYPE_PROGBITS=1;SEC_TYPE_STRTAB=3;LEN_SEC_HEADER=40;SEG_TYPE_LOAD=1;LEN_SEG_HEADER=32
    def __init__(self,name):
        self.name=name
        with open(self.name,_O)as f:self._read_elf_file(f)
    def get_section(self,section_name):
        for s in self.sections:
            if s.name==section_name:return s
        raise ValueError('No section %s in ELF file'%section_name)
    def _read_elf_file(self,f):
        LEN_FILE_HEADER=52
        try:ident,_type,machine,_version,self.entrypoint,_phoff,shoff,_flags,_ehsize,_phentsize,_phnum,shentsize,shnum,shstrndx=struct.unpack('<16sHHLLLLLHHHHHH',f.read(LEN_FILE_HEADER))
        except struct.error as e:raise FatalError('Failed to read a valid ELF header from %s: %s'%(self.name,e))
        if byte(ident,0)!=127 or ident[1:4]!=b'ELF':raise FatalError('%s has invalid ELF magic header'%self.name)
        if machine not in[94,243]:raise FatalError('%s does not appear to be an Xtensa or an RISCV ELF file. e_machine=%04x'%(self.name,machine))
        if shentsize!=self.LEN_SEC_HEADER:raise FatalError('%s has unexpected section header entry size 0x%x (not 0x%x)'%(self.name,shentsize,self.LEN_SEC_HEADER))
        if shnum==0:raise FatalError('%s has 0 section headers'%self.name)
        self._read_sections(f,shoff,shnum,shstrndx);self._read_segments(f,_phoff,_phnum,shstrndx)
    def _read_sections(self,f,section_header_offs,section_header_count,shstrndx):
        f.seek(section_header_offs);len_bytes=section_header_count*self.LEN_SEC_HEADER;section_header=f.read(len_bytes)
        if len(section_header)==0:raise FatalError('No section header found at offset %04x in ELF file.'%section_header_offs)
        if len(section_header)!=len_bytes:raise FatalError('Only read 0x%x bytes from section header (expected 0x%x.) Truncated ELF file?'%(len(section_header),len_bytes))
        section_header_offsets=range(0,len(section_header),self.LEN_SEC_HEADER)
        def read_section_header(offs):name_offs,sec_type,_flags,lma,sec_offs,size=struct.unpack_from('<LLLLLL',section_header[offs:]);return name_offs,sec_type,lma,size,sec_offs
        all_sections=[read_section_header(offs)for offs in section_header_offsets];prog_sections=[s for s in all_sections if s[1]==ELFFile.SEC_TYPE_PROGBITS]
        if not shstrndx*self.LEN_SEC_HEADER in section_header_offsets:raise FatalError('ELF file has no STRTAB section at shstrndx %d'%shstrndx)
        _,sec_type,_,sec_size,sec_offs=read_section_header(shstrndx*self.LEN_SEC_HEADER)
        if sec_type!=ELFFile.SEC_TYPE_STRTAB:print('WARNING: ELF file has incorrect STRTAB section type 0x%02x'%sec_type)
        f.seek(sec_offs);string_table=f.read(sec_size)
        def lookup_string(offs):raw=string_table[offs:];return raw[:raw.index(_I)]
        def read_data(offs,size):f.seek(offs);return f.read(size)
        prog_sections=[ELFSection(lookup_string(n_offs),lma,read_data(offs,size))for(n_offs,_type,lma,size,offs)in prog_sections if lma!=0 and size>0];self.sections=prog_sections
    def _read_segments(self,f,segment_header_offs,segment_header_count,shstrndx):
        f.seek(segment_header_offs);len_bytes=segment_header_count*self.LEN_SEG_HEADER;segment_header=f.read(len_bytes)
        if len(segment_header)==0:raise FatalError('No segment header found at offset %04x in ELF file.'%segment_header_offs)
        if len(segment_header)!=len_bytes:raise FatalError('Only read 0x%x bytes from segment header (expected 0x%x.) Truncated ELF file?'%(len(segment_header),len_bytes))
        segment_header_offsets=range(0,len(segment_header),self.LEN_SEG_HEADER)
        def read_segment_header(offs):seg_type,seg_offs,_vaddr,lma,size,_memsize,_flags,_align=struct.unpack_from('<LLLLLLLL',segment_header[offs:]);return seg_type,lma,size,seg_offs
        all_segments=[read_segment_header(offs)for offs in segment_header_offsets];prog_segments=[s for s in all_segments if s[0]==ELFFile.SEG_TYPE_LOAD]
        def read_data(offs,size):f.seek(offs);return f.read(size)
        prog_segments=[ELFSection(b'PHDR',lma,read_data(offs,size))for(_type,lma,size,offs)in prog_segments if lma!=0 and size>0];self.segments=prog_segments
    def sha256(self):
        sha256=hashlib.sha256()
        with open(self.name,_O)as f:sha256.update(f.read())
        return sha256.digest()
def slip_reader(port,trace_function):
    'Generator to read SLIP packets from a serial port.\n    Yields one full SLIP packet at a time, raises exception on timeout or invalid data.\n\n    Designed to avoid too many calls to serial.read(1), which can bog\n    down on slow systems.\n    ';C='Remaining data in serial buffer: %s';B='Read invalid data: %s';A='Timed out waiting for packet %s';partial_packet=_A;in_escape=_B
    while _C:
        waiting=port.inWaiting();read_bytes=port.read(1 if waiting==0 else waiting)
        if read_bytes==_F:waiting_for='header'if partial_packet is _A else'content';trace_function(A,waiting_for);raise FatalError(A%waiting_for)
        trace_function('Read %d bytes: %s',len(read_bytes),HexFormatter(read_bytes))
        for b in read_bytes:
            if type(b)is int:b=bytes([b])
            if partial_packet is _A:
                if b==_L:partial_packet=_F
                else:trace_function(B,HexFormatter(read_bytes));trace_function(C,HexFormatter(port.read(port.inWaiting())));raise FatalError('Invalid head of packet (0x%s)'%hexify(b))
            elif in_escape:
                in_escape=_B
                if b==b'\xdc':partial_packet+=_L
                elif b==b'\xdd':partial_packet+=_u
                else:trace_function(B,HexFormatter(read_bytes));trace_function(C,HexFormatter(port.read(port.inWaiting())));raise FatalError('Invalid SLIP escape (0xdb, 0x%s)'%hexify(b))
            elif b==_u:in_escape=_C
            elif b==_L:trace_function('Received full packet: %s',HexFormatter(partial_packet));yield partial_packet;partial_packet=_A
            else:partial_packet+=b
def arg_auto_int(x):return int(x,0)
def div_roundup(a,b):' Return a/b rounded up to nearest integer,\n    equivalent result to int(math.ceil(float(int(a)) / float(int(b))), only\n    without possible floating point accuracy errors.\n    ';return(int(a)+int(b)-1)//int(b)
def align_file_position(f,size):' Align the position in the file to the next block of specified size ';align=size-1-f.tell()%size;f.seek(align,1)
def flash_size_bytes(size):
    ' Given a flash size of the type passed in args.flash_size\n    (ie 512KB or 1MB) then return the size in bytes.\n    ';B='KB';A='MB'
    if A in size:return int(size[:size.index(A)])*1024*1024
    elif B in size:return int(size[:size.index(B)])*1024
    else:raise FatalError('Unknown size %s'%size)
def hexify(s,uppercase=_C):
    format_str='%02X'if uppercase else'%02x'
    if not PYTHON2:return ''.join((format_str%c for c in s))
    else:return ''.join((format_str%ord(c)for c in s))
class HexFormatter:
    '\n    Wrapper class which takes binary data in its constructor\n    and returns a hex string as it\'s __str__ method.\n\n    This is intended for "lazy formatting" of trace() output\n    in hex format. Avoids overhead (significant on slow computers)\n    of generating long hex strings even if tracing is disabled.\n\n    Note that this doesn\'t save any overhead if passed as an\n    argument to "%", only when passed to trace()\n\n    If auto_split is set (default), any long line (> 16 bytes) will be\n    printed as separately indented lines, with ASCII decoding at the end\n    of each line.\n    '
    def __init__(self,binary_string,auto_split=_C):self._s=binary_string;self._auto_split=auto_split
    def __str__(self):
        if self._auto_split and len(self._s)>16:
            result='';s=self._s
            while len(s)>0:line=s[:16];ascii_line=''.join((c if c==' 'or c in string.printable and c not in string.whitespace else'.'for c in line.decode('ascii','replace')));s=s[16:];result+='\n    %-16s %-16s | %s'%(hexify(line[:8],_B),hexify(line[8:],_B),ascii_line)
            return result
        else:return hexify(self._s,_B)
def pad_to(data,alignment,pad_character=_m):
    ' Pad to the next alignment boundary ';pad_mod=len(data)%alignment
    if pad_mod!=0:data+=pad_character*(alignment-pad_mod)
    return data
class FatalError(RuntimeError):
    "\n    Wrapper class for runtime errors that aren't caused by internal bugs, but by\n    ESP8266 responses or input content.\n    "
    def __init__(self,message):RuntimeError.__init__(self,message)
    @staticmethod
    def WithResult(message,result):"\n        Return a fatal error object that appends the hex values of\n        'result' as a string formatted argument.\n        ";message+=' (result was %s)'%hexify(result);return FatalError(message)
class NotImplementedInROMError(FatalError):
    '\n    Wrapper class for the error thrown when a particular ESP bootloader function\n    is not implemented in the ROM bootloader.\n    '
    def __init__(self,bootloader,func):FatalError.__init__(self,'%s ROM does not support function %s.'%(bootloader.CHIP_NAME,func.__name__))
class NotSupportedError(FatalError):
    def __init__(self,esp,function_name):FatalError.__init__(self,'Function %s is not supported for %s.'%(function_name,esp.CHIP_NAME))
class UnsupportedCommandError(RuntimeError):
    '\n    Wrapper class for when ROM loader returns an invalid command response.\n\n    Usually this indicates the loader is running in Secure Download Mode.\n    '
    def __init__(self,esp,op):
        if esp.secure_download_mode:msg='This command (0x%x) is not supported in Secure Download Mode'%op
        else:msg='Invalid (unsupported) command 0x%x'%op
        RuntimeError.__init__(self,msg)
def load_ram(esp,args):
    image=LoadFirmwareImage(esp.CHIP_NAME,args.filename);print('RAM boot...')
    for seg in image.segments:
        size=len(seg.data);print('Downloading %d bytes at %08x...'%(size,seg.addr),end=' ');sys.stdout.flush();esp.mem_begin(size,div_roundup(size,esp.ESP_RAM_BLOCK),esp.ESP_RAM_BLOCK,seg.addr);seq=0
        while len(seg.data)>0:esp.mem_block(seg.data[0:esp.ESP_RAM_BLOCK],seq);seg.data=seg.data[esp.ESP_RAM_BLOCK:];seq+=1
        print('done!')
    print('All segments done, executing at %08x'%image.entrypoint);esp.mem_finish(image.entrypoint)
def read_mem(esp,args):print('0x%08x = 0x%08x'%(args.address,esp.read_reg(args.address)))
def write_mem(esp,args):esp.write_reg(args.address,args.value,args.mask,0);print('Wrote %08x, mask %08x to %08x'%(args.value,args.mask,args.address))
def dump_mem(esp,args):
    with open(args.filename,_J)as f:
        for i in range(args.size//4):
            d=esp.read_reg(args.address+i*4);f.write(struct.pack(b'<I',d))
            if f.tell()%1024==0:print_overwrite('%d bytes read... (%d %%)'%(f.tell(),f.tell()*100//args.size))
            sys.stdout.flush()
        print_overwrite('Read %d bytes'%f.tell(),last_line=_C)
    print('Done!')
def detect_flash_size(esp,args):
    if args.flash_size==_A1:
        if esp.secure_download_mode:raise FatalError('Detecting flash size is not supported in secure download mode. Need to manually specify flash size.')
        flash_id=esp.flash_id();size_id=flash_id>>16;args.flash_size=DETECTED_FLASH_SIZES.get(size_id)
        if args.flash_size is _A:print('Warning: Could not auto-detect Flash size (FlashID=0x%x, SizeID=0x%x), defaulting to 4MB'%(flash_id,size_id));args.flash_size=_S
        else:print('Auto-detected Flash size:',args.flash_size)
def _update_image_flash_params(esp,address,args,image):
    ' Modify the flash mode & size bytes if this looks like an executable bootloader image  '
    if len(image)<8:return image
    magic,_,flash_mode,flash_size_freq=struct.unpack('BBBB',image[:4])
    if address!=esp.BOOTLOADER_FLASH_OFFSET:return image
    if(args.flash_mode,args.flash_freq,args.flash_size)==(_D,)*3:return image
    if magic!=esp.ESP_IMAGE_MAGIC:print("Warning: Image file at 0x%x doesn't look like an image file, so not changing any flash settings."%address);return image
    try:test_image=esp.BOOTLOADER_IMAGE(io.BytesIO(image));test_image.verify()
    except Exception:print('Warning: Image file at 0x%x is not a valid %s image, so not changing any flash settings.'%(address,esp.CHIP_NAME));return image
    if args.flash_mode!=_D:flash_mode={_n:0,_A2:1,_A3:2,_A4:3}[args.flash_mode]
    flash_freq=flash_size_freq&15
    if args.flash_freq!=_D:flash_freq={_o:0,_A5:1,_A6:2,_A7:15}[args.flash_freq]
    flash_size=flash_size_freq&240
    if args.flash_size!=_D:flash_size=esp.parse_flash_size_arg(args.flash_size)
    flash_params=struct.pack(b'BB',flash_mode,flash_size+flash_freq)
    if flash_params!=image[2:4]:print('Flash params set to 0x%04x'%struct.unpack('>H',flash_params));image=image[0:2]+flash_params+image[4:]
    return image
def write_flash(esp,args):
    if args.compress is _A and not args.no_compress:args.compress=not args.no_stub
    if args.encrypt or args.encrypt_files is not _A:
        do_write=_C
        if not esp.secure_download_mode:
            if esp.get_encrypted_download_disabled():raise FatalError('This chip has encrypt functionality in UART download mode disabled. This is the Flash Encryption configuration for Production mode instead of Development mode.')
            crypt_cfg_efuse=esp.get_flash_crypt_config()
            if crypt_cfg_efuse is not _A and crypt_cfg_efuse!=15:print('Unexpected FLASH_CRYPT_CONFIG value: 0x%x'%crypt_cfg_efuse);do_write=_B
            enc_key_valid=esp.is_flash_encryption_key_valid()
            if not enc_key_valid:print('Flash encryption key is not programmed');do_write=_B
        files_to_encrypt=args.addr_filename if args.encrypt else args.encrypt_files
        for (address,argfile) in files_to_encrypt:
            if address%esp.FLASH_ENCRYPTED_WRITE_ALIGN:print("File %s address 0x%x is not %d byte aligned, can't flash encrypted"%(argfile.name,address,esp.FLASH_ENCRYPTED_WRITE_ALIGN));do_write=_B
        if not do_write and not args.ignore_flash_encryption_efuse_setting:raise FatalError("Can't perform encrypted flash write, consult Flash Encryption documentation for more information")
    if args.flash_size!=_D:
        flash_end=flash_size_bytes(args.flash_size)
        for (address,argfile) in args.addr_filename:
            argfile.seek(0,os.SEEK_END)
            if address+argfile.tell()>flash_end:raise FatalError('File %s (length %d) at offset %d will not fit in %d bytes of flash. Use --flash-size argument, or change flashing address.'%(argfile.name,argfile.tell(),address,flash_end))
            argfile.seek(0)
    if args.erase_all:erase_flash(esp,args)
    else:
        for (address,argfile) in args.addr_filename:
            argfile.seek(0,os.SEEK_END);write_end=address+argfile.tell();argfile.seek(0);bytes_over=address%esp.FLASH_SECTOR_SIZE
            if bytes_over!=0:print('WARNING: Flash address {:#010x} is not aligned to a {:#x} byte flash sector. {:#x} bytes before this address will be erased.'.format(address,esp.FLASH_SECTOR_SIZE,bytes_over))
            print('Flash will be erased from {:#010x} to {:#010x}...'.format(address-bytes_over,div_roundup(write_end,esp.FLASH_SECTOR_SIZE)*esp.FLASH_SECTOR_SIZE-1))
    ' Create a list describing all the files we have to flash. Each entry holds an "encrypt" flag\n    marking whether the file needs encryption or not. This list needs to be sorted.\n\n    First, append to each entry of our addr_filename list the flag args.encrypt\n    For example, if addr_filename is [(0x1000, "partition.bin"), (0x8000, "bootloader")],\n    all_files will be [(0x1000, "partition.bin", args.encrypt), (0x8000, "bootloader", args.encrypt)],\n    where, of course, args.encrypt is either True or False\n    ';all_files=[(offs,filename,args.encrypt)for(offs,filename)in args.addr_filename];'Now do the same with encrypt_files list, if defined.\n    In this case, the flag is True\n    '
    if args.encrypt_files is not _A:encrypted_files_flag=[(offs,filename,_C)for(offs,filename)in args.encrypt_files];all_files=sorted(all_files+encrypted_files_flag,key=lambda x:x[0])
    for (address,argfile,encrypted) in all_files:
        compress=args.compress
        if compress and encrypted:print('\nWARNING: - compress and encrypt options are mutually exclusive ');print('Will flash %s uncompressed'%argfile.name);compress=_B
        if args.no_stub:print('Erasing flash...')
        image=pad_to(argfile.read(),esp.FLASH_ENCRYPTED_WRITE_ALIGN if encrypted else 4)
        if len(image)==0:print('WARNING: File %s is empty'%argfile.name);continue
        image=_update_image_flash_params(esp,address,args,image);calcmd5=hashlib.md5(image).hexdigest();uncsize=len(image)
        if compress:uncimage=image;image=zlib.compress(uncimage,9);decompress=zlib.decompressobj();blocks=esp.flash_defl_begin(uncsize,len(image),address)
        else:blocks=esp.flash_begin(uncsize,address,begin_rom_encrypted=encrypted)
        argfile.seek(0);seq=0;bytes_sent=0;bytes_written=0;t=time.time();timeout=DEFAULT_TIMEOUT
        while len(image)>0:
            print_overwrite('Writing at 0x%08x... (%d %%)'%(address+bytes_written,100*(seq+1)//blocks));sys.stdout.flush();block=image[0:esp.FLASH_WRITE_SIZE]
            if compress:
                block_uncompressed=len(decompress.decompress(block));bytes_written+=block_uncompressed;block_timeout=max(DEFAULT_TIMEOUT,timeout_per_mb(ERASE_WRITE_TIMEOUT_PER_MB,block_uncompressed))
                if not esp.IS_STUB:timeout=block_timeout
                esp.flash_defl_block(block,seq,timeout=timeout)
                if esp.IS_STUB:timeout=block_timeout
            else:
                block=block+_m*(esp.FLASH_WRITE_SIZE-len(block))
                if encrypted:esp.flash_encrypt_block(block,seq)
                else:esp.flash_block(block,seq)
                bytes_written+=len(block)
            bytes_sent+=len(block);image=image[esp.FLASH_WRITE_SIZE:];seq+=1
        if esp.IS_STUB:esp.read_reg(ESPLoader.CHIP_DETECT_MAGIC_REG_ADDR,timeout=timeout)
        t=time.time()-t;speed_msg=''
        if compress:
            if t>0.0:speed_msg=' (effective %.1f kbit/s)'%(uncsize/t*8/1000)
            print_overwrite('Wrote %d bytes (%d compressed) at 0x%08x in %.1f seconds%s...'%(uncsize,bytes_sent,address,t,speed_msg),last_line=_C)
        else:
            if t>0.0:speed_msg=' (%.1f kbit/s)'%(bytes_written/t*8/1000)
            print_overwrite('Wrote %d bytes at 0x%08x in %.1f seconds%s...'%(bytes_written,address,t,speed_msg),last_line=_C)
        if not encrypted and not esp.secure_download_mode:
            try:
                res=esp.flash_md5sum(address,uncsize)
                if res!=calcmd5:print('File  md5: %s'%calcmd5);print('Flash md5: %s'%res);print('MD5 of 0xFF is %s'%hashlib.md5(_m*uncsize).hexdigest());raise FatalError('MD5 of file does not match data in flash!')
                else:print('Hash of data verified.')
            except NotImplementedInROMError:pass
    print('\nLeaving...')
    if esp.IS_STUB:
        esp.flash_begin(0,0);last_file_encrypted=all_files[-1][2]
        if args.compress and not last_file_encrypted:esp.flash_defl_finish(_B)
        else:esp.flash_finish(_B)
    if args.verify:
        print('Verifying just-written flash...');print('(This option is deprecated, flash contents are now always read back after flashing.)')
        if args.encrypt or args.encrypt_files is not _A:print('WARNING: - cannot verify encrypted files, they will be ignored')
        if not args.encrypt:verify_flash(esp,args)
def image_info(args):
    A='valid';image=LoadFirmwareImage(args.chip,args.filename);print('Image version: %d'%image.version);print('Entry point: %08x'%image.entrypoint if image.entrypoint!=0 else'Entry point not set');print('%d segments'%len(image.segments));print();idx=0
    for seg in image.segments:idx+=1;segs=seg.get_memory_type(image);seg_name=','.join(segs);print('Segment %d: %r [%s]'%(idx,seg,seg_name))
    calc_checksum=image.calculate_checksum();print('Checksum: %02x (%s)'%(image.checksum,A if image.checksum==calc_checksum else'invalid - calculated %02x'%calc_checksum))
    try:
        digest_msg='Not appended'
        if image.append_digest:is_valid=image.stored_digest==image.calc_digest;digest_msg='%s (%s)'%(hexify(image.calc_digest).lower(),A if is_valid else'invalid');print('Validation Hash: %s'%digest_msg)
    except AttributeError:pass
def make_image(args):
    image=ESP8266ROMFirmwareImage()
    if len(args.segfile)==0:raise FatalError('No segments specified')
    if len(args.segfile)!=len(args.segaddr):raise FatalError('Number of specified files does not match number of specified addresses')
    for (seg,addr) in zip(args.segfile,args.segaddr):
        with open(seg,_O)as f:data=f.read();image.segments.append(ImageSegment(addr,data))
    image.entrypoint=args.entrypoint;image.save(args.output)
def elf2image(args):
    e=ELFFile(args.input)
    if args.chip==_Q:print('Creating image for ESP8266...');args.chip=_V
    if args.chip==_W:
        image=ESP32FirmwareImage()
        if args.secure_pad:image.secure_pad=_P
        elif args.secure_pad_v2:image.secure_pad=_G
    elif args.chip==_X:
        image=ESP32S2FirmwareImage()
        if args.secure_pad_v2:image.secure_pad=_G
    elif args.chip==_Y:
        image=ESP32S3BETA2FirmwareImage()
        if args.secure_pad_v2:image.secure_pad=_G
    elif args.chip==_Z:
        image=ESP32S3BETA3FirmwareImage()
        if args.secure_pad_v2:image.secure_pad=_G
    elif args.chip==_a:
        image=ESP32C3FirmwareImage()
        if args.secure_pad_v2:image.secure_pad=_G
    elif args.chip==_b:
        image=ESP32C6BETAFirmwareImage()
        if args.secure_pad_v2:image.secure_pad=_G
    elif args.version==_P:image=ESP8266ROMFirmwareImage()
    else:image=ESP8266V2FirmwareImage()
    image.entrypoint=e.entrypoint;image.flash_mode={_n:0,_A2:1,_A3:2,_A4:3}[args.flash_mode]
    if args.chip!=_V:image.min_rev=int(args.min_rev)
    image.segments=e.segments if args.use_segments else e.sections;image.flash_size_freq=image.ROM_LOADER.FLASH_SIZES[args.flash_size];image.flash_size_freq+={_o:0,_A5:1,_A6:2,_A7:15}[args.flash_freq]
    if args.elf_sha256_offset:image.elf_sha256=e.sha256();image.elf_sha256_offset=args.elf_sha256_offset
    before=len(image.segments);image.merge_adjacent_segments()
    if len(image.segments)!=before:delta=before-len(image.segments);print('Merged %d ELF section%s'%(delta,'s'if delta>1 else''))
    image.verify()
    if args.output is _A:args.output=image.default_output_name(args.input)
    image.save(args.output)
def read_mac(esp,args):
    mac=esp.read_mac()
    def print_mac(label,mac):print('%s: %s'%(label,':'.join(map(lambda x:'%02x'%x,mac))))
    print_mac('MAC',mac)
def chip_id(esp,args):
    try:chipid=esp.chip_id();print('Chip ID: 0x%08x'%chipid)
    except NotSupportedError:print('Warning: %s has no Chip ID. Reading MAC instead.'%esp.CHIP_NAME);read_mac(esp,args)
def erase_flash(esp,args):print('Erasing flash (this may take a while)...');t=time.time();esp.erase_flash();print('Chip erase completed successfully in %.1fs'%(time.time()-t))
def erase_region(esp,args):print('Erasing region (may be slow depending on size)...');t=time.time();esp.erase_region(args.address,args.size);print('Erase completed successfully in %.1f seconds.'%(time.time()-t))
def run(esp,args):esp.run()
def flash_id(esp,args):flash_id=esp.flash_id();print('Manufacturer: %02x'%(flash_id&255));flid_lowbyte=flash_id>>16&255;print('Device: %02x%02x'%(flash_id>>8&255,flid_lowbyte));print('Detected flash size: %s'%DETECTED_FLASH_SIZES.get(flid_lowbyte,'Unknown'))
def read_flash(esp,args):
    if args.no_progress:flash_progress=_A
    else:
        def flash_progress(progress,length):
            msg='%d (%d %%)'%(progress,progress*100.0/length);padding='\x08'*len(msg)
            if progress==length:padding='\n'
            sys.stdout.write(msg+padding);sys.stdout.flush()
    t=time.time();data=esp.read_flash(args.address,args.size,flash_progress);t=time.time()-t;print_overwrite('Read %d bytes at 0x%x in %.1f seconds (%.1f kbit/s)...'%(len(data),args.address,t,len(data)/t*8/1000),last_line=_C)
    with open(args.filename,_J)as f:f.write(data)
def verify_flash(esp,args):
    differences=_B
    for (address,argfile) in args.addr_filename:
        image=pad_to(argfile.read(),4);argfile.seek(0);image=_update_image_flash_params(esp,address,args,image);image_size=len(image);print('Verifying 0x%x (%d) bytes @ 0x%08x in flash against %s...'%(image_size,image_size,address,argfile.name));digest=esp.flash_md5sum(address,image_size);expected_digest=hashlib.md5(image).hexdigest()
        if digest==expected_digest:print('-- verify OK (digest matched)');continue
        else:
            differences=_C
            if getattr(args,'diff','no')!='yes':print('-- verify FAILED (digest mismatch)');continue
        flash=esp.read_flash(address,image_size);assert flash!=image;diff=[i for i in range(image_size)if flash[i]!=image[i]];print('-- verify FAILED: %d differences, first @ 0x%08x'%(len(diff),address+diff[0]))
        for d in diff:
            flash_byte=flash[d];image_byte=image[d]
            if PYTHON2:flash_byte=ord(flash_byte);image_byte=ord(image_byte)
            print('   %08x %02x %02x'%(address+d,flash_byte,image_byte))
    if differences:raise FatalError('Verify failed.')
def read_flash_status(esp,args):print('Status value: 0x%04x'%esp.read_status(args.bytes))
def write_flash_status(esp,args):fmt='0x%%0%dx'%(args.bytes*2);args.value=args.value&(1<<args.bytes*8)-1;print(('Initial flash status: '+fmt)%esp.read_status(args.bytes));print(('Setting flash status: '+fmt)%args.value);esp.write_status(args.value,args.bytes,args.non_volatile);print(('After flash status:   '+fmt)%esp.read_status(args.bytes))
def get_security_info(esp,args):flags,flash_crypt_cnt,key_purposes=esp.get_security_info();print('Flags: 0x%08x (%s)'%(flags,bin(flags)));print('Flash_Crypt_Cnt: 0x%x'%flash_crypt_cnt);print('Key_Purposes: %s'%(key_purposes,))
def merge_bin(args):
    chip_class=_chip_to_rom_loader(args.chip);input_files=sorted(args.addr_filename,key=lambda x:x[0])
    if not input_files:raise FatalError('No input files specified')
    first_addr=input_files[0][0]
    if first_addr<args.target_offset:raise FatalError('Output file target offset is 0x%x. Input file offset 0x%x is before this.'%(args.target_offset,first_addr))
    if args.format!=_A8:raise FatalError("This version of esptool only supports the 'raw' output format")
    with open(args.output,_J)as of:
        def pad_to(flash_offs):of.write(_m*(flash_offs-args.target_offset-of.tell()))
        for (addr,argfile) in input_files:pad_to(addr);image=argfile.read();image=_update_image_flash_params(chip_class,addr,args,image);of.write(image)
        if args.fill_flash_size:pad_to(flash_size_bytes(args.fill_flash_size))
        print('Wrote 0x%x bytes to file %s, ready to flash to offset 0x%x'%(of.tell(),args.output,args.target_offset))
def version(args):print(__version__)
def main(argv=_A,esp=_A):
    '\n    Main function for esptool\n\n    argv - Optional override for default arguments parsing (that uses sys.argv), can be a list of custom arguments\n    as strings. Arguments and their values need to be added as individual items to the list e.g. "-b 115200" thus\n    becomes [\'-b\', \'115200\'].\n\n    esp - Optional override of the connected device previously returned by get_default_connected_device()\n    ';a='--bytes';Z='0';Y='-o';X='--output';W='append';V='-f';U='Suppress progress output';T='--no-progress';S='Address followed by binary filename, separated by space';R='write_flash';Q='value';P='Name of binary dump';O='Size of region to dump';N='?';M='-t';L='no_reset_stub';K='soft_reset';J='-a';I='-e';H='<address> <filename>';G='addr_filename';F='size';E='hard_reset';D='-p';C='filename';B='address';A='store_true';external_esp=esp is not _A;parser=argparse.ArgumentParser(description='esptool.py v%s - ESP8266 ROM Bootloader Utility'%__version__,prog='esptool');parser.add_argument('--chip','-c',help='Target chip type',type=lambda c:c.lower().replace('-',''),choices=[_Q,_V,_W,_X,_Y,_Z,_a,_b],default=os.environ.get('ESPTOOL_CHIP',_Q));parser.add_argument('--port',D,help='Serial port device',default=os.environ.get('ESPTOOL_PORT',_A));parser.add_argument('--baud','-b',help='Serial port baud rate used when flashing/reading',type=arg_auto_int,default=os.environ.get('ESPTOOL_BAUD',ESPLoader.ESP_ROM_BAUD));parser.add_argument('--before',help='What to do before connecting to the chip',choices=[_K,_A9,_e,_d],default=os.environ.get('ESPTOOL_BEFORE',_K));parser.add_argument('--after',J,help='What to do after esptool.py is finished',choices=[E,K,_e,L],default=os.environ.get('ESPTOOL_AFTER',E));parser.add_argument('--no-stub',help='Disable launching the flasher stub, only talk to ROM bootloader. Some features will not be available.',action=A);parser.add_argument('--trace',M,help='Enable trace-level output of esptool.py interactions.',action=A);parser.add_argument('--override-vddsdio',help='Override ESP32 VDDSDIO internal voltage regulator (use with care)',choices=ESP32ROM.OVERRIDE_VDDSDIO_CHOICES,nargs=N);parser.add_argument('--connect-attempts',help='Number of attempts to connect, negative or 0 for infinite. Default: %d.'%DEFAULT_CONNECT_ATTEMPTS,type=int,default=os.environ.get('ESPTOOL_CONNECT_ATTEMPTS',DEFAULT_CONNECT_ATTEMPTS));subparsers=parser.add_subparsers(dest='operation',help='Run esptool {command} -h for additional help')
    def add_spi_connection_arg(parent):parent.add_argument('--spi-connection','-sc',help='ESP32-only argument. Override default SPI Flash connection. Value can be SPI, HSPI or a comma-separated list of 5 I/O numbers to use for SPI flash (CLK,Q,D,HD,CS).',action=SpiConnectionAction)
    parser_load_ram=subparsers.add_parser('load_ram',help='Download an image to RAM and execute');parser_load_ram.add_argument(C,help='Firmware image');parser_dump_mem=subparsers.add_parser('dump_mem',help='Dump arbitrary memory to disk');parser_dump_mem.add_argument(B,help='Base address',type=arg_auto_int);parser_dump_mem.add_argument(F,help=O,type=arg_auto_int);parser_dump_mem.add_argument(C,help=P);parser_read_mem=subparsers.add_parser('read_mem',help='Read arbitrary memory location');parser_read_mem.add_argument(B,help='Address to read',type=arg_auto_int);parser_write_mem=subparsers.add_parser('write_mem',help='Read-modify-write to arbitrary memory location');parser_write_mem.add_argument(B,help='Address to write',type=arg_auto_int);parser_write_mem.add_argument(Q,help='Value',type=arg_auto_int);parser_write_mem.add_argument('mask',help='Mask of bits to write',type=arg_auto_int,nargs=N,default='0xFFFFFFFF')
    def add_spi_flash_subparsers(parent,allow_keep,auto_detect):
        ' Add common parser arguments for SPI flash properties ';extra_keep_args=[_D]if allow_keep else[]
        if auto_detect and allow_keep:extra_fs_message=', detect, or keep'
        elif auto_detect:extra_fs_message=', or detect'
        elif allow_keep:extra_fs_message=', or keep'
        else:extra_fs_message=''
        parent.add_argument('--flash_freq','-ff',help='SPI Flash frequency',choices=extra_keep_args+[_o,_A5,_A6,_A7],default=os.environ.get('ESPTOOL_FF',_D if allow_keep else _o));parent.add_argument('--flash_mode','-fm',help='SPI Flash mode',choices=extra_keep_args+[_n,_A2,_A3,_A4],default=os.environ.get('ESPTOOL_FM',_D if allow_keep else _n));parent.add_argument('--flash_size','-fs',help='SPI Flash size in MegaBytes (1MB, 2MB, 4MB, 8MB, 16M) plus ESP8266-only (256KB, 512KB, 2MB-c1, 4MB-c1)'+extra_fs_message,action=FlashSizeAction,auto_detect=auto_detect,default=os.environ.get('ESPTOOL_FS',_D if allow_keep else _R));add_spi_connection_arg(parent)
    parser_write_flash=subparsers.add_parser(R,help='Write a binary blob to flash');parser_write_flash.add_argument(G,metavar=H,help=S,action=AddrFilenamePairAction);parser_write_flash.add_argument('--erase-all',I,help='Erase all regions of flash (not just write areas) before programming',action=A);add_spi_flash_subparsers(parser_write_flash,allow_keep=_C,auto_detect=_C);parser_write_flash.add_argument(T,D,help=U,action=A);parser_write_flash.add_argument('--verify',help='Verify just-written data on flash (mostly superfluous, data is read back during flashing)',action=A);parser_write_flash.add_argument('--encrypt',help='Apply flash encryption when writing data (required correct efuse settings)',action=A);parser_write_flash.add_argument('--encrypt-files',metavar=H,help='Files to be encrypted on the flash. Address followed by binary filename, separated by space.',action=AddrFilenamePairAction);parser_write_flash.add_argument('--ignore-flash-encryption-efuse-setting',help='Ignore flash encryption efuse settings ',action=A);compress_args=parser_write_flash.add_mutually_exclusive_group(required=_B);compress_args.add_argument('--compress','-z',help='Compress data in transfer (default unless --no-stub is specified)',action=A,default=_A);compress_args.add_argument('--no-compress','-u',help='Disable data compression during transfer (default if --no-stub is specified)',action=A);subparsers.add_parser('run',help='Run application code in flash');parser_image_info=subparsers.add_parser('image_info',help='Dump headers from an application image');parser_image_info.add_argument(C,help='Image file to parse');parser_make_image=subparsers.add_parser('make_image',help='Create an application image from binary files');parser_make_image.add_argument('output',help='Output image file');parser_make_image.add_argument('--segfile',V,action=W,help='Segment input file');parser_make_image.add_argument('--segaddr',J,action=W,help='Segment base address',type=arg_auto_int);parser_make_image.add_argument('--entrypoint',I,help='Address of entry point',type=arg_auto_int,default=0);parser_elf2image=subparsers.add_parser('elf2image',help='Create an application image from ELF file');parser_elf2image.add_argument('input',help='Input ELF file');parser_elf2image.add_argument(X,Y,help='Output filename prefix (for version 1 image), or filename (for version 2 single image)',type=str);parser_elf2image.add_argument('--version',I,help='Output image version',choices=[_P,_G],default=_P);parser_elf2image.add_argument('--min-rev','-r',help='Minimum chip revision',choices=[Z,_P,_G,'3'],default=Z);parser_elf2image.add_argument('--secure-pad',action=A,help='Pad image so once signed it will end on a 64KB boundary. For Secure Boot v1 images only.');parser_elf2image.add_argument('--secure-pad-v2',action=A,help='Pad image to 64KB, so once signed its signature sector will start at the next 64K block. For Secure Boot v2 images only.');parser_elf2image.add_argument('--elf-sha256-offset',help='If set, insert SHA256 hash (32 bytes) of the input ELF file at specified offset in the binary.',type=arg_auto_int,default=_A);parser_elf2image.add_argument('--use_segments',help='If set, ELF segments will be used instead of ELF sections to genereate the image.',action=A);add_spi_flash_subparsers(parser_elf2image,allow_keep=_B,auto_detect=_B);subparsers.add_parser('read_mac',help='Read MAC address from OTP ROM');subparsers.add_parser(_AH,help='Read Chip ID from OTP ROM');parser_flash_id=subparsers.add_parser('flash_id',help='Read SPI flash manufacturer and device ID');add_spi_connection_arg(parser_flash_id);parser_read_status=subparsers.add_parser('read_flash_status',help='Read SPI flash status register');add_spi_connection_arg(parser_read_status);parser_read_status.add_argument(a,help='Number of bytes to read (1-3)',type=int,choices=[1,2,3],default=2);parser_write_status=subparsers.add_parser('write_flash_status',help='Write SPI flash status register');add_spi_connection_arg(parser_write_status);parser_write_status.add_argument('--non-volatile',help='Write non-volatile bits (use with caution)',action=A);parser_write_status.add_argument(a,help='Number of status bytes to write (1-3)',type=int,choices=[1,2,3],default=2);parser_write_status.add_argument(Q,help='New value',type=arg_auto_int);parser_read_flash=subparsers.add_parser('read_flash',help='Read SPI flash content');add_spi_connection_arg(parser_read_flash);parser_read_flash.add_argument(B,help='Start address',type=arg_auto_int);parser_read_flash.add_argument(F,help=O,type=arg_auto_int);parser_read_flash.add_argument(C,help=P);parser_read_flash.add_argument(T,D,help=U,action=A);parser_verify_flash=subparsers.add_parser('verify_flash',help='Verify a binary blob against flash');parser_verify_flash.add_argument(G,help='Address and binary file to verify there, separated by space',action=AddrFilenamePairAction);parser_verify_flash.add_argument('--diff','-d',help='Show differences',choices=['no','yes'],default='no');add_spi_flash_subparsers(parser_verify_flash,allow_keep=_C,auto_detect=_C);parser_erase_flash=subparsers.add_parser('erase_flash',help='Perform Chip Erase on SPI flash');add_spi_connection_arg(parser_erase_flash);parser_erase_region=subparsers.add_parser('erase_region',help='Erase a region of the flash');add_spi_connection_arg(parser_erase_region);parser_erase_region.add_argument(B,help='Start address (must be multiple of 4096)',type=arg_auto_int);parser_erase_region.add_argument(F,help='Size of region to erase (must be multiple of 4096)',type=arg_auto_int);parser_merge_bin=subparsers.add_parser('merge_bin',help='Merge multiple raw binary files into a single file for later flashing');parser_merge_bin.add_argument(X,Y,help='Output filename',type=str,required=_C);parser_merge_bin.add_argument('--format',V,help='Format of the output file',choices=_A8,default=_A8);add_spi_flash_subparsers(parser_merge_bin,allow_keep=_C,auto_detect=_B);parser_merge_bin.add_argument('--target-offset',M,help='Target offset where the output file will be flashed',type=arg_auto_int,default=0);parser_merge_bin.add_argument('--fill-flash-size',help='If set, the final binary file will be padded with FF bytes up to this flash size.',action=FlashSizeAction);parser_merge_bin.add_argument(G,metavar=H,help=S,action=AddrFilenamePairAction);subparsers.add_parser('version',help='Print esptool version');subparsers.add_parser('get_security_info',help='Get some security-related data')
    for operation in subparsers.choices.keys():assert operation in globals(),'%s should be a module function'%operation
    argv=expand_file_arguments(argv or sys.argv[1:]);args=parser.parse_args(argv);print('esptool.py v%s'%__version__)
    if args.operation is _A:parser.print_help();sys.exit(1)
    if args.operation==R and args.encrypt and args.encrypt_files is not _A:raise FatalError('Options --encrypt and --encrypt-files must not be specified at the same time.')
    operation_func=globals()[args.operation]
    if PYTHON2:operation_args=inspect.getargspec(operation_func).args
    else:operation_args=inspect.getfullargspec(operation_func).args
    if operation_args[0]=='esp':
        if args.before!=_d:initial_baud=min(ESPLoader.ESP_ROM_BAUD,args.baud)
        else:initial_baud=args.baud
        if args.port is _A:ser_list=get_port_list();print('Found %d serial ports'%len(ser_list))
        else:ser_list=[args.port]
        esp=esp or get_default_connected_device(ser_list,port=args.port,connect_attempts=args.connect_attempts,initial_baud=initial_baud,chip=args.chip,trace=args.trace,before=args.before)
        if esp is _A:raise FatalError('Could not connect to an Espressif device on any of the %d available serial ports.'%len(ser_list))
        if esp.secure_download_mode:print('Chip is %s in Secure Download Mode'%esp.CHIP_NAME)
        else:print('Chip is %s'%esp.get_chip_description());print('Features: %s'%_v.join(esp.get_chip_features()));print('Crystal is %dMHz'%esp.get_crystal_freq());read_mac(esp,args)
        if not args.no_stub:
            if esp.secure_download_mode:print('WARNING: Stub loader is not supported in Secure Download Mode, setting --no-stub');args.no_stub=_C
            else:esp=esp.run_stub()
        if args.override_vddsdio:esp.override_vddsdio(args.override_vddsdio)
        if args.baud>initial_baud:
            try:esp.change_baud(args.baud)
            except NotImplementedInROMError:print("WARNING: ROM doesn't support changing baud rate. Keeping initial baud rate %d"%initial_baud)
        if hasattr(args,'spi_connection')and args.spi_connection is not _A:
            if esp.CHIP_NAME!=_AG:raise FatalError('Chip %s does not support --spi-connection option.'%esp.CHIP_NAME)
            print('Configuring SPI flash mode...');esp.flash_spi_attach(args.spi_connection)
        elif args.no_stub:print('Enabling default SPI flash mode...');esp.flash_spi_attach(0)
        if hasattr(args,'flash_size'):
            print('Configuring flash size...');detect_flash_size(esp,args)
            if args.flash_size!=_D:esp.flash_set_parameters(flash_size_bytes(args.flash_size))
        try:operation_func(esp,args)
        finally:
            try:
                for (address,argfile) in args.addr_filename:argfile.close()
            except AttributeError:pass
        if operation_func==load_ram:print('Exiting immediately.')
        elif args.after==E:esp.hard_reset()
        elif args.after==K:print('Soft resetting...');esp.soft_reset(_B)
        elif args.after==L:print('Staying in flasher stub.')
        else:
            print('Staying in bootloader.')
            if esp.IS_STUB:esp.soft_reset(_C)
        if not external_esp:esp._port.close()
    else:operation_func(args)
def get_port_list():
    if list_ports is _A:raise FatalError('Listing all serial ports is currently not available. Please try to specify the port when running esptool.py or update the pyserial package to the latest version')
    return sorted((ports.device for ports in list_ports.comports()))
def expand_file_arguments(argv):
    ' Any argument starting with "@" gets replaced with all values read from a text file.\n    Text file arguments can be split by newline or by space.\n    Values are added "as-is", as if they were specified in this order on the command line.\n    ';new_args=[];expanded=_B
    for arg in argv:
        if arg.startswith('@'):
            expanded=_C
            with open(arg[1:],'r')as f:
                for line in f.readlines():new_args+=shlex.split(line)
        else:new_args.append(arg)
    if expanded:print('esptool.py %s'%' '.join(new_args[1:]));return new_args
    return argv
class FlashSizeAction(argparse.Action):
    " Custom flash size parser class to support backwards compatibility with megabit size arguments.\n\n    (At next major relase, remove deprecated sizes and this can become a 'normal' choices= argument again.)\n    "
    def __init__(self,option_strings,dest,nargs=1,auto_detect=_B,**kwargs):super(FlashSizeAction,self).__init__(option_strings,dest,nargs,**kwargs);self._auto_detect=auto_detect
    def __call__(self,parser,namespace,values,option_string=_A):
        try:value={'2m':_p,'4m':_q,'8m':_R,'16m':_c,'32m':_S,'16m-c1':_AC,'32m-c1':_AD}[values[0]];print("WARNING: Flash size arguments in megabits like '%s' are deprecated."%values[0]);print("Please use the equivalent size '%s'."%value);print('Megabit arguments may be removed in a future release.')
        except KeyError:value=values[0]
        known_sizes=dict(ESP8266ROM.FLASH_SIZES);known_sizes.update(ESP32ROM.FLASH_SIZES)
        if self._auto_detect:known_sizes[_A1]=_A1;known_sizes[_D]=_D
        if value not in known_sizes:raise argparse.ArgumentError(self,'%s is not a known flash size. Known sizes: %s'%(value,_v.join(known_sizes.keys())))
        setattr(namespace,self.dest,value)
class SpiConnectionAction(argparse.Action):
    " Custom action to parse 'spi connection' override. Values are SPI, HSPI, or a sequence of 5 pin numbers separated by commas.\n    "
    def __call__(self,parser,namespace,value,option_string=_A):
        if value.upper()=='SPI':value=0
        elif value.upper()=='HSPI':value=1
        elif','in value:
            values=value.split(',')
            if len(values)!=5:raise argparse.ArgumentError(self,'%s is not a valid list of comma-separate pin numbers. Must be 5 numbers - CLK,Q,D,HD,CS.'%value)
            try:values=tuple((int(v,0)for v in values))
            except ValueError:raise argparse.ArgumentError(self,'%s is not a valid argument. All pins must be numeric values'%values)
            if any([v for v in values if v>33 or v<0]):raise argparse.ArgumentError(self,'Pin numbers must be in the range 0-33.')
            clk,q,d,hd,cs=values;value=hd<<24|cs<<18|d<<12|q<<6|clk
        else:raise argparse.ArgumentError(self,'%s is not a valid spi-connection value. Values are SPI, HSPI, or a sequence of 5 pin numbers CLK,Q,D,HD,CS).'%value)
        setattr(namespace,self.dest,value)
class AddrFilenamePairAction(argparse.Action):
    ' Custom parser class for the address/filename pairs passed as arguments '
    def __init__(self,option_strings,dest,nargs='+',**kwargs):super(AddrFilenamePairAction,self).__init__(option_strings,dest,nargs,**kwargs)
    def __call__(self,parser,namespace,values,option_string=_A):
        pairs=[]
        for i in range(0,len(values),2):
            try:address=int(values[i],0)
            except ValueError:raise argparse.ArgumentError(self,'Address "%s" must be a number'%values[i])
            try:argfile=open(values[i+1],_O)
            except IOError as e:raise argparse.ArgumentError(self,e)
            except IndexError:raise argparse.ArgumentError(self,'Must be pairs of an address and the binary filename to write there')
            pairs.append((address,argfile))
        end=0
        for (address,argfile) in sorted(pairs,key=lambda x:x[0]):
            argfile.seek(0,2);size=argfile.tell();argfile.seek(0);sector_start=address&~(ESPLoader.FLASH_SECTOR_SIZE-1);sector_end=(address+size+ESPLoader.FLASH_SECTOR_SIZE-1&~(ESPLoader.FLASH_SECTOR_SIZE-1))-1
            if sector_start<end:message='Detected overlap at address: 0x%x for file: %s'%(address,argfile.name);raise argparse.ArgumentError(self,message)
            end=sector_end
        setattr(namespace,self.dest,pairs)
ESP32ROM.STUB_CODE=eval(zlib.decompress(base64.b64decode(b'\neNqVWm1z2zYS/iu0EtmRL+kAFEUCvutEdh3ZTtKp3Tayk1PvSoJgk7uMR3Z0Y9lN/vthX0CAlJrefZBNguBid7H77Av4+97Krld7B0m1t1gL5X5isW6y54u1NNENXLQ3ZbZY28rd1DAtPMkP4XLHXZfu1yzWRiQwAlRT96zRneEn7k+WJKvFWrulbOpuc/ebhNWEgLcm9JaS7n/eoeBYAdqOHaWI+xLGhCNpRRBHVIMGWHCjhZsKNDKgA5zKDkFN02TtRkUktUpY9EbFojrO4f26x5RjxnEAM5V4PD+lpziz/F9m9leHnxRJuxNJb0/wpzxHFtRlvHgVkRSGtBEWZkmRqypSsO5xqNP3dBFGUNXz+01RHMXPbjQFaQYiSWhrtokjxJT4tZ5ZN8/tiy4DK7aOFGf6bOmeQF2utq9Jmu6PCUlvG8EWDQT8DydkSc+8kRt1BDw/IqstQesqCGJKsmQFs0D7oFjchbEbdFZY8r1SA7c86w99yQ1KDazCn7EQKxsMB5cZs47wTUkKb5opk5BA370qWXVejQbojnmM1VnCddPfSL24ph2o5f+pdQPWjAIotjE9/pZecYpgIQ0+OkIBDpQehN3SItoFoaZ8pXobp7L4fjr1V6c0jO/orCXlHaOSfosSxgjQZlWwJG7TrN+0CGl0dN2Ci2bRVewXVTqKMIE3ye9sZ6YGiGFs0vwDRJLOEY0mxg2PtS+Z9IrfcFzqKsbA9KzvotEC6Epl4MYv5rWK1wXs7YwnZ0FyO4kMhc0b14+NwCCIVdGrSA/teEYwI8RnIgBPpCNg5QxtJdrWngEGverFqiXTHb/uvrUipuuIUUQlNMtIOazILkicPwNPZlhyfyrQTXOVnY8NuTRsqRi7d2X27qfzxeKQQgm9bTkiol8eO4XlvAMYmx6zz0/IWUGrdryJehDMJOx5TThR1SS2ap28G9dae1TmYEC3Jhv9/ASoHAxG8O9JBgSM0DEQq24EQQdaUgRuyuenj1ERMHdAKilDZKk9ctQEnCoC58Dat7AziAMpgY31WyHJOMs0WL8HPgxJkrRkZeR9abBB71a9AFriVRKPpx+8V+6wsiKsQ67FNoU+AszdkgBAKPDhQHhEATLKe0tKbJCEpzAB5DOHPoSmcQqAI3KkkMMMLGCwPxZ/O+QYkY6u9GkcVJ4hJINCS7/Rkz6XT4mJNrSCEniu4A1L2TVhmqVdgOd1xSqptqjEzzFs7uMubXzX01RMp/gKnZrnZJtzNqM2SXLgU8M0PEML4ntZDRiqUADmpsn+KJXy11fxjQOoGvF/CgjyDfsAoFg7DBkxyOtu8p0d4gFipGRb8FE6lsnp7Dpe/yIktOje8vw7wx6fRbskw7StHt+Y/fBWxc64wc8GZrwcTMdgD/Mxpa3oFLzrVfQ2QHRZdvOwDh9RPMA6oQrv4EYUZLVkLjmxF5SANmz+eMODuRgWxVR/Zi4f4818H98s45tVfLOOb0CpvzEe1qJ1JljvPbvVThky5DhblmVzRnJKBLoqaBL9OXu6uH4LhI4anhJlFUGki1CPoMw+MS7fQMSaXLo9Umz1ud+ImtbF+dvMr93AG3fRsgtMPlwgS7PdaCZu6fQT6V0yaPvaisxsufb2WnTt1civRijgz/FQN1FFMvkRI+DDDQpxG3n3xNv/ElK0JAQcjJ1oDMPgIp4diWle8ivE34S4qja0/DB4OC4Ido3lRBfpnK+2y60KkHRM+GfjwsCAb5fp7COTEbF+4cmToNt6I97MOTGzlDyVPsvE/f1IZKrKUkRC+yw4RwXDyF8sVu8ofy3TVx4I33B9Ow4+XTa8RkGZhlHNC2Dhx11YAjQBFXd6SSqBHAS8W6OOfwUFJqRYxAMWRm9ggg1Aga/n28DBhrwHozwa+FNfblTm6z6OKbF5/sPp4Rnx2fYgQNmQMohqShkUkoAbEZoYWE9kz3u1Xa8QxN0y3XpDimmncu2nrcgVIXF74xS2F1HIou6JTzw8Cx1JhIqIVKEe+vyel7aMUVc++5x+3MdYpVIOWdKhDV0ZQ1ev6R+kohMmA9iiSZw1xTpBtu1i21WLfa8pzgP2kX9j+Vvb1oWvByXCGuc+HkZsfyPhLQQfSeEUX2/DwhHEVPFqUKRs4xN6lxZ5RS5hZOI8vi7Y9em5rznsjr9w8aPGsLf/qN/VqdqSdoe9xLKB4O/dWcAyj1JBgkNCmab5kQwcOI/7WM6KzwchAx1x6i+/bMdkX6N0VGTyjf5YSltVleRKJptCvQwB1KSUHGIchQ4FdOUQKdjl4go+Xh6UVrF8teSek4kGEYwUqQdqglLGcBdHpg735L6HDN52fwxRMPuLT2lbsU4pK5D5qiPtFScLhmugoMfx0SHIfMS9QYmT9nBQ3l4sVqOLXSr4MQSY4o4oSMs2hgz6t8e3fIFdqmOgsTxOGrw4HXaYzM4uZt3MRZrHRxcL7hzUaYhkEPHJSAkrwYINbvHX170hx3FhbgnocEIoLdNuAoTUxiE6wDj4WBmN4xzcwFkUZMVtR2nohLqgH3KSU3wQ4k7Pm998ckGZABpLfpc0PNlwnkHjJ6DZBlEunf3GOsox4DUnOOiWQbhpLgNdg0g4e9xaFPaUYpZglbIglkCZzV0YN0SPKg7i3/cgcooVAZkbT3IWmjywXtVZb+ZhkNM37Te6x5XIz4ml/YbJIUu/+OmL1Z0OYAJ5oeXUqKvu44jd1qho3e5ytrW4k3j497aqQB/3zR6srNK+IqdBi9E0QVNk8fdoUPrBD8ScyLpRrp047kt0GVX1MCHrT5hz9wD1cesNfkam7crWs94z4xnMgtlg6ilGFz7QYEaYJtzCy3xTL4UQQbeWQ2e44GyIISv2VlEdW58pzrk0rX1e3P5gm4o5BFclt5VEmOaO2YDrfnqxS60C1CtS4r4xbuLREOWHv+MlYFkSenVVNiejbeyQPMOhxTFlKNhjK4mS7z63aTe0HpS82sg6D0C6Ww6lUByVnLg4VN8L1USbird9wjm1tBo7C/ZR8q/BtunFxmL7VJQr+Veua8Qd77A84PSelgczhQhuoh5DT5TTraLoTVHmrCfY+nzJMEim5beBsU+Z2DjZgQuGupL3DQ0K4DU3ES0h+VFLK/cdpyGJUOJFaskcOOVpm57byMS61/DAxY8hH0DYNPFSpCdkEfTKF15WvYGwPQz2BSGhFL51feutcx3ZswavyJ6evqAoL31c7/gjLlf65VCA9SQsuFxRlghAbiHoasuJAB6M1dQttnJUH2FCcP+Y6AJiqPEX1Ej+76h1rugtvyfOU1fxHubYNL0Gy1+/BeLDI4orDSS2mvvncfzU4gFOInwG46EK0l3VbyuVVA8BVGDdoqgtj+N4AiJO9qmzgnUaBKSJj/HcVYe5Uk7hIvGP8sjGKPqzaWftlIITA+zkiOOkFYZBD9BK4+kDlqI/cVaKiXCkLkzds02FrQhRtcr8uJpT0xVPhVKMTOufgdfXccZDc2IHkUUXoE1+yl6kYOfVfyC9fQMLvAArmbBZKr+3tgucjvR1a6Czoxhtd4/9Cs99rTTstcXFJkEM5E1UrpWgj16MmbOD2yl4wCGlcHry/eL6M3W00TJsZBl4nKWp0ATvg/2ANKHmNjjWcD1WNBem0Lcxcj3iHr/GAoAb1yU3Lky6s7jdJUMQNupl2/QZtrE+wzXgp0SoK+N+d6mhfSKrYzIAw0G/KjKqFER7zNAcB8jSapu/i9mjziYEfDKUbkEtxPiq1Zv1kA8GQYtYwth7uPsSMM1/SgCVBdYtNlgSpMmial2YegsinXIage3KW7hY0jKyn0uYfP42nNthFWL9I1K0246Tb19OaUxmsQ1g9HSs+vOKpj1peqD0VasPr+5w5WtmDWIWMGBgSmlOCrqV+YgSbzzZrajbhuV8/hAKBVk0FIFkcchxwa4ZOvBx65yjKFkoD4lsY5eEO2o8avkmZSz9dw8pNogs1fq4uXgCYsgwac7En1sMGXIh7QG7q3RbZfn41GA5NT1Bhyfjmd1jgl2HxLxAd9uNoiEeFFm69s7ozGDvjks1nPYJxoEyQi3KHz0X+QEWC46YL9Y9ptUpcvWDv83w9tOMqzLbS14Rg8BNsRV2NQyOrHyry/aKfeX5h0k1JxNRbeh9e7qCzMBDOlV2YnqCCdwxH5Ogos655cjApdNNtMCYNeaD0pbdTXDDTmskY42JBvCBF1mcg6jhkJAOU+7KhDcrH7/GR3FpCj1Hjl9VldA07L4zg3jwpOy/QhMHE2f7J4Uu5NV17OV4aCSO+XAVUj/IhUGpkhuVYAfaH6LF5z8abRwL4J/96dtOe6S1x0cpufduBN09AlrwIgDqcvJ6y/krvQyim8yLPqEkQIFD9DdD5i97J23gx/qSDzWLl9xI1iSUUsX0FKzljFNM7Y0s3ao8/0XHEk83jyJmUm4veJDGFCOJLF5eAuWbt6D1K/zGSN+cY+p9X653Pazc8YkCdgcOiFEsBdk7Sgxz9vtwPG3kzTnxXPrjWvkD95f5NArxiE+6scJSdOqHebUfM7426Wj0F8CzSu3CWjckfS1DFC3ZOA2e/u/6c18eoIBawuG74qMomCwqqhIx1E4S+p6Ab7HfuQxEsStaEojgwabXr6W6ueRmvPQfi/gTmGKL7ibv2DaZe5Lm3n+lAElAU681QU2pnFp1I89DBlvnIz4lwGCbEwyWEz4pkhl7FNpdcfZmFn0hA4ai21rmI+kZuIYBzc1BaJEECursavYhoIPmjlGYoM/ezho/4XQ4z4jVcASUbUE0mXgY/WbzqZKyPzj7R7sC2HrO3YSwyNM/g83dLRmmiBoa+SVro3N62H7P5xdqy4cn4ZstzHLwU6o0fN6gWKs+YRL4GdD4M/VOQxa5S9NFsTugrVC9SnfjVF2MHvl10WMHnnADbStac9Cezu+Fz7OINZz9jI+wtp3c1/5jPS9a3nl1EFjp6mrvaYIfjf7z06q8hU9HpSiySeqUmLkn9np1e98OyonM3WBdrsroG1M+4djjJzGhcS4mkyz78l9OLzRi')))
def _main():
    try:main()
    except FatalError as e:print('\nA fatal error occurred: %s'%e);sys.exit(2)
if __name__=='__main__':_main()