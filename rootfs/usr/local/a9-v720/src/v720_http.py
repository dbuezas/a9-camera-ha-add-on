
from __future__ import annotations
from datetime import datetime
import email.utils
import random
import json
import os
import subprocess
import threading
import uuid

from queue import Queue, Empty
import socket
from log import log

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import netifaces
from netcl_udp import netcl_udp
import v720_sta

TCP_PORT = 6123
HTTP_PORT = 80


class v720_http(log, BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    _dev_lst = {}
    _dev_hnds = {}

    @staticmethod
    def add_dev(dev):
        # if dev.id not in v720_http._dev_lst:
        v720_http._dev_lst[dev.id] = dev

    @staticmethod
    def rm_dev(dev):
        if dev.id in v720_http._dev_lst:
            del v720_http._dev_lst[dev.id]

    @staticmethod
    def serve_forever(_http_port=HTTP_PORT):
        try:
            with ThreadingHTTPServer(("", _http_port), v720_http) as httpd:
                httpd.socket.setsockopt(
                    socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)
                try:
                    httpd.serve_forever()
                except KeyboardInterrupt:
                    print('exiting..')
                    exit(0)
        except PermissionError:
            print(
                f'--- Can\'t open {_http_port} port due to system root permissions or maybe you have already running HTTP server?')
            print(
                f'--- if not try to use "sudo sysctl -w net.ipv4.ip_unprivileged_port_start={_http_port}"')
            exit(1)

    def __new__(cls, *args, **kwargs) -> v720_http:
        ret = super(v720_http, cls).__new__(cls)
        cls._dev_hnds["stream"] = ret.__stream_hnd
        cls._dev_hnds["snapshot"] = ret.__snapshot_hnd
        return ret

    def __init__(self, request, client_address, server) -> None:
        log.__init__(self, 'HTTP')
        try:
            BaseHTTPRequestHandler.__init__(
                self, request, client_address, server)
        except ConnectionResetError:
            self.err(f'Connection closed by peer @ ({self.client_address[0]})')

    def __stream_hnd(self, dev: v720_sta):
        id = str(uuid.uuid4())
        audio_fifo_path = '/tmp/audio_fifo_'+id
        video_fifo_path = '/tmp/video_fifo_'+id

        os.mkfifo(audio_fifo_path)
        os.mkfifo(video_fifo_path)

        command = ['ffmpeg',
                   '-rtbufsize', '0',
                   '-use_wallclock_as_timestamps', '1',
                   '-f', 'alaw', '-ar', '8000', '-ac', '1', '-i', audio_fifo_path,
                   '-rtbufsize', '0',
                   '-use_wallclock_as_timestamps', '1',
                   '-f', 'mjpeg', '-i', video_fifo_path,
                   '-c:v', 'copy', '-c:a', 'aac',
                   '-f', 'matroska', 'pipe:1',
                   '-loglevel', 'verbose',
                   ]

        ffmpeg = subprocess.Popen(command, stdout=subprocess.PIPE)

        def track_thread(q: Queue, pipe_path: str):
            pipe = os.open(pipe_path, os.O_WRONLY)
            while True:
                frame = q.get(timeout=15)
                if (frame == None):
                    os.close(pipe)
                    os.unlink(pipe_path)
                    break
                os.write(pipe, frame)
        audio_queue = Queue(1024)
        video_queue = Queue(1024)

        audio_thread = threading.Thread(
            target=track_thread, args=(audio_queue, audio_fifo_path))
        video_thread = threading.Thread(
            target=track_thread, args=(video_queue, video_fifo_path))
        audio_thread.start()
        video_thread.start()

        def _on_audio_frame(dev, frame):
            audio_queue.put_nowait(frame)

        def _on_video_frame(dev, frame):
            video_queue.put_nowait(frame)

        dev.set_aframe_cb(_on_audio_frame)
        dev.set_vframe_cb(_on_video_frame)

        def ffmpeg_cb(q: Queue, ffmpeg):
            while True:
                q.put(ffmpeg.stdout.read1(128))
        out_queue = Queue(1024)

        out_thread = threading.Thread(
            target=ffmpeg_cb, args=(out_queue, ffmpeg))
        out_thread.start()
        try:
            self.warn(
                f'Live stream request @ {dev.id} ({self.client_address[0]})')
            dev.cap_live()
            self.send_response(200)
            self.send_header('Content-type', 'video/mp4')
            self.end_headers()
            while not self.wfile.closed:
                frame = out_queue.get(timeout=15)
                if (frame == None):
                    break
                self.wfile.write(frame)

        except Empty:
            # tODO timeout stdout read
            self.err('Camera request timeout')
            self.send_response(
                502, f'Camera request timeout {dev.id}@{dev.host}:{dev.port}')
        except BrokenPipeError:
            self.err(
                f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')
        finally:
            audio_queue.put(None)
            video_queue.put(None)
            dev.cap_stop()
            dev.unset_vframe_cb(_on_video_frame)
            dev.unset_aframe_cb(_on_audio_frame)
            ffmpeg.kill()

        try:
            self.send_header('Content-length', 0)
            self.send_header('Connection', 'close')
            self.end_headers()
        except BrokenPipeError:
            self.err(
                f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')

    def __snapshot_hnd(self, dev):
        self.warn(f'Snapshot request @ {dev.id} ({self.client_address[0]})')
        q = Queue(1)

        def _on_video_frame(dev, frame):
            q.put(frame)

        dev.set_vframe_cb(_on_video_frame)
        try:
            dev.cap_live()
            img = q.get(timeout=5)
            self.send_response(200)
            self.send_header('Content-type', 'image/jpeg')
            self.send_header('Content-length', len(img))
            self.send_header('Connection', 'close')
            self.end_headers()
            self.wfile.write(img)

        except Empty:
            self.err('Camera request timeout')
            self.send_response(
                502, f'Camera request timeout {dev.id}@{dev.host}:{dev.port}')
        except (BrokenPipeError, ConnectionResetError):
            self.err(
                f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')
        finally:
            dev.unset_vframe_cb(_on_video_frame)
            dev.cap_stop()

    def do_GET(self):
        _path = self.path[1:].split('/')
        if len(_path) >= 3 and \
                _path[0] == 'dev' and \
                _path[1] in v720_http._dev_lst:
            _cmd = _path[2]

            if _cmd in self._dev_hnds:
                _dev = v720_http._dev_lst[_path[1]]
                self._dev_hnds[_cmd](_dev)
            if _cmd == 'send':
                # /dev/88322349423934/send/1 or 0
                dev: v720_sta = v720_http._dev_lst[_path[1]]
                x = _cmd = _path[3]
                import cmd_udp
                cmd = {
                    'code': cmd_udp.CODE_FORWARD_DEV_IR_LED,
                    'IrLed': int(x)
                }
                dev.send_command(cmd)
        else:
            self.info(f'GET unknown path: {self.path}')
            self.send_error(404, 'Not found')

    def do_POST(self):
        ret = None
        hdr = [
            'HTTP/1.1 200',
            'Server: nginx/1.14.0 (Ubuntu)',
            f'Date: {email.utils.format_datetime(datetime.now())}',
            'Content-Type: application/json',
            'Connection: keep-alive',
        ]
        self.info(f'POST {self.path}')
        if self.path.startswith('/app/api/ApiSysDevicesBatch/registerDevices'):
            ret = {"code": 200, "message": "OK",
                   "data": f"0800c00{random.randint(0,99999):05d}"}
        elif self.path.startswith('/app/api/ApiSysDevicesBatch/confirm'):
            ret = {"code": 200, "message": "OK", "data": None}
        elif self.path.startswith('/app/api/ApiSysDevices/a9bindingAppDevice'):
            ret = {"code": 200, "message": "OK", "data": None}
        elif self.path.startswith('/app/api/ApiServer/getA9ConfCheck'):
            uid = f'{random.randint(0,99999):05d}'
            p = self.path[len('/app/api/ApiServer/getA9ConfCheck?'):]
            for param in p.split('&'):
                if param.startswith('devicesCode'):
                    uid = param.split('=')[1]

            gws = netifaces.gateways()
            ret = {
                "code": 200,
                "message": "OK",
                "data": {
                    "tcpPort": TCP_PORT,
                    "uid": uid,
                    "isBind": "8",
                    "domain": "v720.naxclow.com",
                    "updateUrl": None,
                    "host": netcl_udp.get_ip(list(gws['default'].values())[0][0], 80),
                    "currTime": f'{int(datetime.timestamp(datetime.now()))}',
                    "pwd": "deadbeef",
                    "version": None
                }
            }

        if ret is not None:
            ret = json.dumps(ret)
            hdr.append(f'Content-Length: {len(ret)}')
            hdr.append('\r\n')
            hdr.append(ret)
            resp = '\r\n'.join(hdr)
            self.info(f'sending: {resp}')
            self.wfile.write(resp.encode('utf-8'))
        else:
            self.err(f'Unknown POST query @ {self.path}')
            self.send_response(404)
            self.send_header('Content-type', 'application/json')
            self.send_header('Connection', 'close')
            self.end_headers()
            self.wfile.write(b'Unknown POST request')


if __name__ == '__main__':
    try:
        with ThreadingHTTPServer(("", HTTP_PORT), v720_http) as httpd:
            httpd.socket.setsockopt(socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)
            try:
                httpd.serve_forever()
            except KeyboardInterrupt:
                print('exiting..')
                exit(0)
    except PermissionError:
        print(
            f'--- Can\'t open {HTTP_PORT} port due to system root permissions or maybe you have already running HTTP server?')
        print(
            f'--- if not try to use "sudo sysctl -w net.ipv4.ip_unprivileged_port_start=80"')
