#!/usr/bin/env python3
import fcntl, struct, ctypes, glob, os, sys, time
USBDEVFS_CONTROL = 0xC0185500
def find():
    for d in glob.glob('/sys/bus/usb/devices/*'):
        try:
            v=open(d+'/idVendor').read().strip(); p=open(d+'/idProduct').read().strip()
            if v=='16c0' and p=='05dc' and open(d+'/product').read().strip()=='Fan-Controller':
                b=int(open(d+'/busnum').read()); n=int(open(d+'/devnum').read())
                return '/dev/bus/usb/%03d/%03d'%(b,n)
        except OSError: pass
def ctrl(fd, req, val=0, idx=0):
    buf=ctypes.create_string_buffer(256)
    s=struct.pack('BBHHHIxxxxQ',0xC0,req,val,idx,256,1000,ctypes.addressof(buf))
    n=fcntl.ioctl(fd,USBDEVFS_CONTROL,bytearray(s))
    return buf.raw[:max(n,0)] if isinstance(n,int) else b''
def status(fd):
    r=ctrl(fd,6)
    return dict(mode=r[0], short_s=int.from_bytes(r[3:5],'little')*3, long_s=int.from_bytes(r[5:7],'little')*3, resets=r[7]) if len(r)>=8 else r
if __name__=='__main__':
    mode=sys.argv[1] if len(sys.argv)>1 else 'run'
    while True:
        path=find()
        if not path:
            if mode!='run': sys.exit('no Octominer fan controller found')
            time.sleep(10); continue
        try:
            fd=os.open(path,os.O_RDWR)
            if mode=='status': print(status(fd)); sys.exit(0)
            if mode=='feed': ctrl(fd,0); print('fed', status(fd)); sys.exit(0)
            print('feeding', path, status(fd), flush=True)
            while True:
                ctrl(fd,0); time.sleep(5)
        except OSError as e:
            print('feed error:', e, flush=True)
            if mode!='run': sys.exit(1)
            time.sleep(2)
