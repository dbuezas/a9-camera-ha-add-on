
from __future__ import annotations
from datetime import datetime
import email.utils
import random
import json
import os
import subprocess
import threading
import uuid
from urllib.parse import urlparse, parse_qs

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
        cls._dev_hnds["browser-stream"] = ret.__browser_stream_hnd
        cls._dev_hnds["go2rtc-stream"] = ret.__go2rtc_stream_hnd
        cls._dev_hnds["audio"] = ret.__audio_stream_hnd
        cls._dev_hnds["live"] = ret.__live_hnd
        cls._dev_hnds["snapshot"] = ret.__snapshot_hnd
        cls._dev_hnds["cmd"] = ret.__cmd_hnd
        return ret

    def __init__(self, request, client_address, server) -> None:
        log.__init__(self, 'HTTP')
        try:
            BaseHTTPRequestHandler.__init__(
                self, request, client_address, server)
        except ConnectionResetError:
            self.err(f'Connection closed by peer @ ({self.client_address[0]})')

    def __browser_stream_hnd(self, dev: v720_sta):
        get_command = lambda audio_fifo_path,video_fifo_path:['ffmpeg',
                    '-thread_queue_size', '512',
                    '-use_wallclock_as_timestamps', '1',
                    '-f', 'alaw', '-ar', '8000', '-ac', '1', '-channel_layout', 'mono', '-i', audio_fifo_path,
                    '-use_wallclock_as_timestamps', '1',
                    '-f', 'mjpeg', 
                    '-framerate', '10', # input fps
                    '-i', video_fifo_path,
                    '-c:v', 'libvpx', '-b:v', '256k', '-deadline', 'realtime',
                    '-c:a', 'libopus', '-b:a', '16k', '-af', 'adelay=0ms', 
                    '-framerate', '10', # output fps
                    '-f', 'webm', 'pipe:1',
                   ]
        return self.__stream(dev,get_command)
    
    def __go2rtc_stream_hnd(self, dev: v720_sta):
        get_command = lambda audio_fifo_path,video_fifo_path:['ffmpeg',
                   '-thread_queue_size', '512',
                   '-use_wallclock_as_timestamps', '1',
                   '-f', 'alaw', '-ar', '8000', '-ac', '1', '-channel_layout', 'mono', '-i', audio_fifo_path,
                   '-use_wallclock_as_timestamps', '1',
                   '-f', 'mjpeg', '-i', video_fifo_path,
                   '-c:v', 'copy',
                   '-c:a', 'libopus', '-b:a', '16k', '-af', 'adelay=0ms', 
                   '-f', 'matroska', 'pipe:1',
                   ]
        return self.__stream(dev,get_command)
    
    def __audio_stream_hnd(self, dev: v720_sta):
        get_command = lambda audio_fifo_path,video_fifo_path:['ffmpeg',
                   '-thread_queue_size', '512',
                   '-use_wallclock_as_timestamps', '1',
                   '-f', 'alaw', '-ar', '8000', '-ac', '1', '-channel_layout', 'mono', '-i', audio_fifo_path,
                   '-c:a', 'mp3', '-b:a', '32k', '-af', 'adelay=0ms', 
                   '-f', 'mp3', 'pipe:1',
                   ]
        return self.__stream(dev,get_command)
        
    
    def __stream(self, dev: v720_sta, get_command ):
        self.warn(f'Live stream request @ {dev.id} ({self.client_address[0]})')
        id = str(uuid.uuid4())
        audio_fifo_path = '/tmp/audio_fifo_'+id
        video_fifo_path = '/tmp/video_fifo_'+id
        command = get_command(audio_fifo_path, video_fifo_path)
        os.mkfifo(audio_fifo_path)
        os.mkfifo(video_fifo_path)
        
        ffmpeg = subprocess.Popen(command, stdout=subprocess.PIPE)

        def track_cb(q: Queue, pipe_path: str):
            pipe = os.open(pipe_path, os.O_WRONLY)
            while True:
                frame = q.get(timeout=10)
                if (frame == None):
                    os.close(pipe)
                    os.unlink(pipe_path)
                    break
                os.write(pipe, frame)
        def ffmpeg_cb(q: Queue, ffmpeg):
            while True:
                q.put(ffmpeg.stdout.read1(512))
                
        audio_queue = Queue(1024)
        video_queue = Queue(1024)
        out_queue = Queue(1024)

        audio_thread = threading.Thread(target=track_cb, args=(audio_queue, audio_fifo_path))
        audio_thread.start()
        video_thread = threading.Thread(target=track_cb, args=(video_queue, video_fifo_path))
        video_thread.start()
        threading.Thread(target=ffmpeg_cb, args=(out_queue, ffmpeg)).start()
        def _on_audio_frame(dev, frame):
            audio_queue.put_nowait(frame)

        def _on_video_frame(dev, frame):
            video_queue.put_nowait(frame)

        dev.set_aframe_cb(_on_audio_frame)
        dev.set_vframe_cb(_on_video_frame)

        try:
            dev.cap_live()
            self.send_response(200)
            self.send_header('Content-type', 'video/mp4')
            self.end_headers()
            while not self.wfile.closed:
                frame = out_queue.get(timeout=10)
                self.wfile.write(frame)

        except Empty:
            self.err('Camera request timeout')
            self.send_response(502, f'Camera request timeout {dev.id}@{dev.host}:{dev.port}')
        except BrokenPipeError:
            self.err(f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')
        except KeyboardInterrupt:
            self.err(f'Keyboard closing @ {dev.id} ({self.client_address[0]})')
            
        finally:
            dev.cap_stop()
            dev.unset_vframe_cb(_on_video_frame)
            dev.unset_aframe_cb(_on_audio_frame)
            ffmpeg.kill()
            audio_queue.put(None)
            video_queue.put(None)
            audio_thread.join()
            video_thread.join()

        try:
            self.send_header('Content-length', 0)
            self.send_header('Connection', 'close')
            self.end_headers()
        except BrokenPipeError:
            self.err(f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')

    def __live_hnd(self, dev):
        q = Queue(1024) # 15kb * 1024 ~ 15mb per camera
        def _on_video_frame(dev, frame):
            q.put(frame)

        dev.set_vframe_cb(_on_video_frame)
        
        try:
            self.warn(f'Live video request @ {dev.id} ({self.client_address[0]})')
            self.send_response(200)
            self.send_header('Connection', 'keep-alive')
            self.send_header('Age', 0)
            self.send_header('Pragma', 'no-cache')
            self.send_header('Content-type', 'multipart/x-mixed-replace; boundary="jpgboundary"')
            self.end_headers()
            dev.cap_live()
            while not self.wfile.closed:
                img = q.get(timeout=5)
                self.wfile.write(b"--jpgboundary\r\n")
                self.send_header('Content-type', 'image/jpeg')
                # self.send_header('Content-length', len(img))
                self.end_headers()
                self.wfile.write(img)
                self.wfile.write(b'\r\n')

        except Empty:
            self.err('Camera request timeout')
            self.send_response(502, f'Camera request timeout {dev.id}@{dev.host}:{dev.port}')
        except BrokenPipeError:
            self.err(f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')
        finally:
            dev.unset_vframe_cb(_on_video_frame)
            dev.cap_stop()

        try:
            self.send_header('Content-length', 0)
            self.send_header('Connection', 'close')
            self.end_headers()
        except BrokenPipeError:
            self.err(f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')

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
            self.send_response(502, f'Camera request timeout {dev.id}@{dev.host}:{dev.port}')
        except (BrokenPipeError, ConnectionResetError):
            self.err(f'Connection closed by peer @ {dev.id} ({self.client_address[0]})')
        finally:
            dev.unset_vframe_cb(_on_video_frame)
            dev.cap_stop()
    def __cmd_hnd(self, dev: v720_sta):
        self.warn(f'Cmd request @ {dev.id} ({self.client_address[0]})')
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')  # for ingress
        self.send_header('Connection', 'close')
        self.end_headers()
        
        parsed_path = urlparse(self.path)
        params = parse_qs(parsed_path.query)
        # Convert the parameters to a dictionary with single values
        cmd = {}
        for k, v in params.items():
            # Check if the value is a digit and convert accordingly
            cmd[k] = int(v[0]) if v[0].isdigit() else v[0]

        self.wfile.write(json.dumps(cmd).encode('utf-8'))
        dev.send_command(cmd)

    def __dev_list(self):
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')  # for ingress
        self.send_header('Connection', 'close')
        self.end_headers()
        _devs = []
        for _id in v720_http._dev_lst.keys():
            _dev = v720_http._dev_lst[_id]
            _devs.append({
                'host': _dev.host,
                'port': _dev.port,
                'uid': _id
            })

        self.wfile.write(json.dumps(_devs).encode('utf-8'))
    
    def __homepage(self):
        self.info(f'GET device list: {self.path}')
        self.send_response(200)
        self.send_header('Content-type','text/html')
        self.send_header('Connection', 'close')
        self.end_headers()

        html_file_path = os.path.join(os.path.dirname(__file__), 'homepage.html')
        with open(html_file_path, 'r', encoding='utf-8') as file:
            self.wfile.write(bytes(file.read(), "utf8"))

    def do_GET(self):
        url = urlparse(self.path)
        _path = url.path[1:].split('/')
        if len(_path) == 1:
            self.__homepage()
        elif self.path.startswith('/dev/list'):
            self.__dev_list()
        elif len(_path) == 3 and \
                _path[0] == 'dev' and \
                _path[1] in v720_http._dev_lst:
            _cmd = _path[2]

            if _cmd in self._dev_hnds:
                _dev = v720_http._dev_lst[_path[1]]
                self._dev_hnds[_cmd](_dev)
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
        self.warn(f'POST {self.path}')
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
