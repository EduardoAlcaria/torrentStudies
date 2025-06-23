import hashlib
import random
import re
import urllib
import requests
import socket
import struct

peers = []

def encode(obj):
    if isinstance(obj, str):
        return str(len(obj)).encode() + b":" + obj.encode()
    elif isinstance(obj, int):
        return b"i" + str(obj).encode() + b"e"
    elif isinstance(obj, bytes):
        return str(len(obj)).encode() + b":" + obj
    elif isinstance(obj, list):
        return b"l" + b"".join(encode(i) for i in obj) + b"e"
    elif isinstance(obj, dict):
        items = sorted(obj.items())
        return b"d" + b"".join(encode(k) + encode(v) for k, v in items) + b"e"
    raise ValueError("Allowed types: int, bytes, list, dict; not %s", type(obj))


def decode_first(s):
    if s.startswith(b"i"):
        match = re.match(b"i(-?\\d+)e", s)
        return int(match.group(1)), s[match.end():]

    elif s.startswith(b"l"):
        lst = []
        rest = s[1:]
        while not rest.startswith(b"e"):
            item, rest = decode_first(rest)
            lst.append(item)
        return lst, rest[1:]

    elif s.startswith(b"d"):
        dct = {}
        rest = s[1:]
        while not rest.startswith(b"e"):
            key, rest = decode_first(rest)
            val, rest = decode_first(rest)
            dct[key] = val
        return dct, rest[1:]

    elif s[0:1].isdigit():
        m = re.match(b"(\\d+):", s)
        if not m:
            raise ValueError(f"Invalid string format at: {s[:20]}")
        length = int(m.group(1))
        start = m.end()
        end = start + length
        return s[start:end], s[end:]

    else:
        raise ValueError(f"Invalid Bencode at: {s[:20]}")

def udp_tracker_announce(tracker_host, tracker_port, info_hash, peer_id, port, uploaded, downloaded, left):
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(5)
    

    protocol_id = 0x41727101980 
    action_connect = 0
    transaction_id = random.randint(0, 0xFFFFFFFF)
    
    connect_packet = struct.pack(">QII", protocol_id, action_connect, transaction_id)
    sock.sendto(connect_packet, (tracker_host, tracker_port))
    
    try:
        data, _ = sock.recvfrom(16)
    except socket.timeout:
        raise Exception("UDP tracker connect request timed out")
    
    action, recv_transaction_id, connection_id = struct.unpack(">IIQ", data)
    if action != action_connect or recv_transaction_id != transaction_id:
        raise Exception("UDP tracker connect response invalid")
    
    action_announce = 1
    transaction_id = random.randint(0, 0xFFFFFFFF)
    downloaded = int(downloaded)
    left = int(left)
    uploaded = int(uploaded)
    event = 2  
    
    ip = 0 
    key = random.randint(0, 0xFFFFFFFF)
    num_want = -1  
    packed_peer_id = peer_id.encode('ascii')  
    
    announce_packet = struct.pack(">QII20s20sQQQIIIiH", 
                                  connection_id,
                                  action_announce,
                                  transaction_id,
                                  info_hash,
                                  packed_peer_id,
                                  downloaded,
                                  left,
                                  uploaded,
                                  event,
                                  ip,
                                  key,
                                  num_want,
                                  port)
    sock.sendto(announce_packet, (tracker_host, tracker_port))
    
    try:
        data, _ = sock.recvfrom(4096)
    except socket.timeout:
        raise Exception("UDP tracker announce request timed out")
    
    action_resp, trans_resp, interval, leechers, seeders = struct.unpack(">IIIII", data[:20])
    if action_resp != action_announce or trans_resp != transaction_id:
        raise Exception("UDP tracker announce response invalid")
    
    peers_binary = data[20:]
    peers = []
    for i in range(0, len(peers_binary), 6):
        ip_bytes = peers_binary[i:i+4]
        port_bytes = peers_binary[i+4:i+6]
        ip_str = '.'.join(str(b) for b in ip_bytes)
        port_num = struct.unpack(">H", port_bytes)[0]
        peers.append((ip_str, port_num))
    
    return {
        "interval": interval,
        "leechers": leechers,
        "seeders": seeders,
        "peers": peers
    }

def handshake(peers, info_hash, peer_id):
    valid_hanshakes = []
    handshake_message = (
        b'\x13' +
        b'BitTorrent protocol' +
        b'\x00' * 8 +
        info_hash +                 
        peer_id.encode('ascii')  
    )

    for ip, port_num in peers:
        try:
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.settimeout(5)
            sock.connect((ip, port_num))
            sock.sendall(handshake_message)
            
            print(f"\033[1;33mHandshake sent to: {ip}:{port_num}\033[m")

            response = sock.recv(68)

            if len(response) != 68:
                print(f"\033[1;31mInvalid response length from {ip}:{port_num}: {len(response)} bytes\033[m")
                sock.close()
                continue
            


            print(f"\033[1;33mReceived response: {response.hex()}\033[m")

            if response[28:48] == info_hash:

                print(f"\033[1;33mValid handshake with {ip}:{port_num}\033[m")

                if response[0] == 19 and response[1:20] == b'BitTorrent protocol':

                    print(f"\033[1;33mValid peer found: {ip}:{port_num}\033[m")
                    valid_hanshakes.append((ip, port_num))
                    print(f"\033[1;33mSending the interest: {ip}:{port_num}\033[m")


                    interest_message = b'\x00\x00\x00\x01\x02'

                    sock.sendall(interest_message)
                    
                    print(f"{"\033[1;33m"}Interest sent to: {ip}:{port_num} {"\033[m"}")

                    print(f"{"\033[1;33m"}Response: {response.hex()} {"\033[m"}")


                    while True:
                        try:
                            lenght_bytes = recv_all(sock, 4)
                            lenght = int.from_bytes(lenght_bytes, byteorder='big')

                            unchoke_response = recv_all(sock, lenght)

                            if lenght == 0:
                                print(f"{"\033[1;33m"} alive {"\033[m"}")
                                continue  
                            msg_id = unchoke_response[0]
                            
                            payload = unchoke_response[1:]
        
                            if msg_id == 1:
                                print(f"{"\033[1;33m"}Unchoked by: {ip}:{port_num} {"\033[m"}")
                                break
                            elif msg_id == 0:
                                print(f"{"\033[1;33m"}Choked by: {ip}:{port_num} {"\033[m"}")
                                break
                            elif msg_id == 4:
                                print(f"{"\033[1;33m"}Have message received from: {ip}:{port_num} {"\033[m"}")
                                break
                            elif msg_id == 5:
                                print(f"{"\033[1;33m"}Bitfield message received from: {ip}:{port_num} {"\033[m"}")
                                parsed_bitfield = bitfieldParser(payload)
                    
                                if parsed_bitfield:

                                    index_to_request = parsed_bitfield[0]
                                    begin = 0
                                    lenght = 2**14

                                    send_request(sock, index_to_request, begin, lenght)
                                    print(f"{"\033[1;33m"}Request sent for piece {index_to_request} from {ip}:{port_num} {"\033[m"}")
                                else:
                                    print(f"{"\033[1;33m"}No piece received {"\033[m"}")

                                break
                            elif msg_id == 7:
                                print(f"{"\033[1;33m"}Piece received from: {ip}:{port_num} {"\033[m"}")
                                break
                            else:
                                print(f"{"\033[1;33m"}Unknown message received from: {ip}:{port_num} {"\033[m"}")
                                break

                        except Exception as e:
                            print(f"{"\033[1;33m"}Error receiving data from {ip}:{port_num}: {e} {"\033[m"}")
                            break
                    
                       
            sock.close()
            
        except socket.error as e:
        
            print(f"Failed to connect to {ip}:{port_num}: {e}")


def bitfieldParser(payload):
    pieces = []
    for byte_index, byte in enumerate(payload):
        for bit_index in range(8):
            if byte & (1 << (7 - bit_index)):
                pieces.append(byte_index * 8 + bit_index)
    return pieces     
  
def send_request(sock, index, begin, length):
    msg = struct.pack(">IBIII", 13, 6, index, begin, length)
    sock.sendall(msg)

def receive_piece(sock):
    lenght_prefix = recv_all(sock, 4)
    lenght = int.from_bytes(lenght_prefix, byteorder='big')

    if lenght == 0:
        print("Received keep-alive message")
        return None
    payload = recv_all(sock, lenght)
    msg_id = payload[0]

    if msg_id != 7:
        raise ValueError(f"Expected message ID 7 (piece), got {msg_id}")
        return None
    index = int.from_bytes(payload[1:5], byteorder='big')
    begin = int.from_bytes(payload[5:9], byteorder='big')
    block = payload[9:]

    print(f"Received piece: index={index}, begin={begin}, block_length={len(block)}")

    return index, begin, block


def recv_all(scock, lenght):
    data = b''
    while len(data) < lenght:
        chuck = scock.recv(lenght - len(data))
        if not chuck:
            raise Exception("Connection closed before receiving all data")
        data += chuck
    return data

def main():
    with open("Dying Light 2 [FitGirl Repack].torrent", "rb") as f:
        raw_data = f.read()

    decoded_dict, leftover = decode_first(raw_data)

    info_dict = decoded_dict[b'info']

    if b'length' in info_dict:
        file_length = info_dict[b'length']  # single file torrent
    elif b'files' in info_dict:
        file_length = sum(file[b'length'] for file in info_dict[b'files'])
    else:
        raise ValueError("Torrent info missing length data.")

    bencoded_info = encode(info_dict)
    info_hash = hashlib.sha1(bencoded_info).digest()
    print("Info Hash:", info_hash.hex())

    peer_id = '-ED0001-' + ''.join(random.choices('0123456789ABCDEF', k=12))
    peer_id = peer_id.ljust(20, '0')
    
    
    tracker_url = decoded_dict[b"announce"].decode()


    if tracker_url.startswith("udp://"):
    
        import urllib.parse
        parsed = urllib.parse.urlparse(tracker_url)
        tracker_host = parsed.hostname
        tracker_port = parsed.port
        print(f"Contacting UDP tracker {tracker_host}:{tracker_port}")

        response = udp_tracker_announce(
            tracker_host=tracker_host,
            tracker_port=tracker_port,
            info_hash=info_hash,
            peer_id=peer_id,
            port=6881,
            uploaded=0,
            downloaded=0,
            left=file_length
        )
        print("UDP Tracker response:")
        peers = response.get("peers")
        handshake(peers, info_hash, peer_id)

    elif tracker_url.startswith("http://") or tracker_url.startswith("https://"):
        info_hash_encoded = urllib.parse.quote_from_bytes(info_hash)
        peer_id_parsed = urllib.parse.quote(peer_id)

        params = {
            "info_hash": info_hash_encoded,
            "peer_id": peer_id_parsed,
            "port": 6881,
            "uploaded": 0,
            "downloaded": 0,
            "left": file_length,
            "compact": 1,
            "event": "started"
        }
        url = tracker_url + "?" + urllib.parse.urlencode(params)

        response = requests.get(url, timeout=10)
        print("HTTP Tracker response:")
        print(response.content)

    else:
        print(f"Unsupported tracker protocol: {tracker_url}")

main()
