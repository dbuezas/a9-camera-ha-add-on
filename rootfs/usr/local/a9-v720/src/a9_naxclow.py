#!/usr/bin/env python3

import argparse
from log import log
import logging

from v720_sta import start_srv

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    arg_gr = parser.add_mutually_exclusive_group(required=True)
    arg_gr.add_argument('-s', '--server', action='store_true',
                        help='Start a fake-server', default=False)
    parser.add_argument('-v', '--verbose', action='store_true',
                        help='Enable debug logs', default=False)
    parser.add_argument('--proxy-port', type=int,
                        help='HTTP server port, use for proxying it via NGINX, etc', default=80)
    args = parser.parse_args()

    if not args.verbose:
        log.set_log_lvl(logging.WARN)

    if args.server:
        print(f'''-------- A9 V720 fake-server starting. --------
\033[92mStream: http://127.0.0.1:{args.proxy_port}/dev/[CAM-ID]/stream
Snapshot: http://127.0.0.1:{args.proxy_port}/dev/[CAM-ID]/snapshot\033[0m
''')
        start_srv(args.proxy_port)
