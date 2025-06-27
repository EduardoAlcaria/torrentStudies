import hashlib
import struct
import socket
import threading
import time
import os
import sys
import random
import urllib.parse
import urllib.request
from typing import List, Optional, Tuple, Set
import bencodepy

class TorrentFile:
    
    def __init__(self, path: List[str], length: int, offset: int = 0):
        self.path = path
        self.length = length
        self.offset = offset 
        self.file_handle = None

class Torrent:
    
    def __init__(self, torrent_path: str):
        with open(torrent_path, 'rb') as f:
            self.torrent_data = bencodepy.decode(f.read())
        
        self.info = self.torrent_data[b'info']
        self.info_hash = hashlib.sha1(bencodepy.encode(self.info)).digest()
        self.piece_length = self.info[b'piece length']
        self.pieces = self.info[b'pieces']
        self.num_pieces = len(self.pieces) // 20
        
       
        if b'files' in self.info:
       
            self.files = []
            offset = 0
            for file_info in self.info[b'files']:
                path = [p.decode('utf-8') for p in file_info[b'path']]
                length = file_info[b'length']
                self.files.append(TorrentFile(path, length, offset))
                offset += length
            self.total_length = offset
            self.name = self.info[b'name'].decode('utf-8')
        else:
          
            self.files = [TorrentFile([self.info[b'name'].decode('utf-8')], self.info[b'length'])]
            self.total_length = self.info[b'length']
            self.name = self.info[b'name'].decode('utf-8')
        
        self.announce = self.torrent_data[b'announce'].decode('utf-8')
        
     
        self.announce_list = []
        if b'announce-list' in self.torrent_data:
            for tier in self.torrent_data[b'announce-list']:
                tier_trackers = []
                for tracker in tier:
                    tier_trackers.append(tracker.decode('utf-8'))
                self.announce_list.append(tier_trackers)
        else:
            
            self.announce_list = [[self.announce]]
        
    def get_piece_hash(self, piece_index: int) -> bytes:
        
        start = piece_index * 20
        return self.pieces[start:start + 20]

class Peer:
    
    def __init__(self, ip: str, port: int, info_hash: bytes, peer_id: bytes):
        self.ip = ip
        self.port = port
        self.info_hash = info_hash
        self.peer_id = peer_id
        self.socket = None
        self.connected = False
        self.choked = True
        self.interested = False
        self.peer_choking = True
        self.peer_interested = False
        self.bitfield = None
        
    def connect(self) -> bool:
        
        try:
            self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            self.socket.settimeout(5) 
            self.socket.connect((self.ip, self.port))
            
       
            handshake = self._create_handshake()
            self.socket.send(handshake)
            
         
            response = self.socket.recv(68)
            if len(response) != 68:
                self.socket.close()
                return False
                
        
            if response[1:20] != b'BitTorrent protocol':
                self.socket.close()
                return False
            if response[28:48] != self.info_hash:
                self.socket.close()
                return False
                
            self.connected = True
            self.socket.settimeout(10)  
            return True
            
        except Exception as e:
            if self.socket:
                self.socket.close()
            return False
    
    def _create_handshake(self) -> bytes:
        
        protocol = b'BitTorrent protocol'
        reserved = b'\x00' * 8
        return struct.pack('B', 19) + protocol + reserved + self.info_hash + self.peer_id
    
    def send_message(self, message_id: int, payload: bytes = b'') -> bool:
        
        if not self.connected:
            return False
        try:
            if message_id == -1:   
                message = struct.pack('>I', 0)
            else:
                length = len(payload) + 1
                message = struct.pack('>IB', length, message_id) + payload
            self.socket.send(message)
            return True
        except:
            return False
    
    def receive_message(self) -> Optional[Tuple[int, bytes]]:
       
        if not self.connected:
            return None
        try:
            
            length_data = self._recv_exact(4)
            if not length_data:
                return None
            length = struct.unpack('>I', length_data)[0]
            
            if length == 0: 
                return (-1, b'')
            
          
            message_data = self._recv_exact(length)
            if not message_data:
                return None
            
            message_id = message_data[0]
            payload = message_data[1:]
            return (message_id, payload)
        except:
            return None
    
    def _recv_exact(self, length: int) -> Optional[bytes]:
        
        data = b''
        while len(data) < length:
            try:
                chunk = self.socket.recv(length - len(data))
                if not chunk:
                    return None
                data += chunk
            except socket.timeout:
                return None
            except:
                return None
        return data
    
    def request_piece(self, piece_index: int, begin: int, length: int) -> bool:
        
        payload = struct.pack('>III', piece_index, begin, length)
        return self.send_message(6, payload) 
    
    def send_interested(self) -> bool:
        
        return self.send_message(2)  
    
    def disconnect(self):
       
        if self.socket:
            try:
                self.socket.close()
            except:
                pass
        self.connected = False

class PieceManager:

    def __init__(self, torrent: Torrent):
        self.torrent = torrent
        self.pieces = [False] * torrent.num_pieces
        self.piece_data = {}
        self.pending_requests = {} 
        self.piece_blocks = {} 
        self.lock = threading.Lock()
        
    def get_next_piece(self, peer_id: str) -> Optional[int]:
        with self.lock:
            for i, downloaded in enumerate(self.pieces):
                if not downloaded and i not in self.pending_requests:
                    self.pending_requests[i] = peer_id
                    self.piece_blocks[i] = {}
                    return i
            return None
    
    def verify_piece(self, piece_index: int, data: bytes) -> bool:
        
        piece_hash = hashlib.sha1(data).digest()
        expected_hash = self.torrent.get_piece_hash(piece_index)
        return piece_hash == expected_hash
    
    def add_block(self, piece_index: int, begin: int, data: bytes) -> Optional[bytes]:
        
        with self.lock:
            if piece_index not in self.piece_blocks:
                self.piece_blocks[piece_index] = {}
            
           
            self.piece_blocks[piece_index][begin] = data
            
           
            expected_length = self.torrent.piece_length
            if piece_index == self.torrent.num_pieces - 1:
               
                expected_length = self.torrent.total_length - (piece_index * self.torrent.piece_length)
            
           
            piece_data = b''
            current_pos = 0
            
            while current_pos < expected_length:
                if current_pos in self.piece_blocks[piece_index]:
                    block = self.piece_blocks[piece_index][current_pos]
                    piece_data += block
                    current_pos += len(block)
                else:
                    
                    return None
            
            
            if len(piece_data) == expected_length:
                return piece_data
            else:
                return None
    
    def store_piece(self, piece_index: int, data: bytes) -> bool:
      
        if not self.verify_piece(piece_index, data):
            print(f"Piece {piece_index} failed verification!")
            with self.lock:
               
                if piece_index in self.pending_requests:
                    del self.pending_requests[piece_index]
                if piece_index in self.piece_blocks:
                    del self.piece_blocks[piece_index]
            return False
        
        with self.lock:
            self.piece_data[piece_index] = data
            self.pieces[piece_index] = True
            if piece_index in self.pending_requests:
                del self.pending_requests[piece_index]
            if piece_index in self.piece_blocks:
                del self.piece_blocks[piece_index]
        
        print(f"✓ Downloaded and verified piece {piece_index + 1}/{self.torrent.num_pieces}")
        return True
    
    def release_piece(self, piece_index: int):
       
        with self.lock:
            if piece_index in self.pending_requests:
                del self.pending_requests[piece_index]
            if piece_index in self.piece_blocks:
                del self.piece_blocks[piece_index]
    
    def is_complete(self) -> bool:
       
        return all(self.pieces)
    
    def get_progress(self) -> float:
       
        return (sum(self.pieces) / len(self.pieces)) * 100

class FileManager:
    
    def __init__(self, torrent: Torrent, download_dir: str):
        self.torrent = torrent
        self.download_dir = download_dir
        self.file_handles = {}
        
    def create_files(self):
        
        base_path = os.path.join(self.download_dir, self.torrent.name)
        os.makedirs(base_path, exist_ok=True)
        
        for torrent_file in self.torrent.files:
            file_path = os.path.join(base_path, *torrent_file.path)
            os.makedirs(os.path.dirname(file_path), exist_ok=True)
            
           
            with open(file_path, 'wb') as f:
                f.truncate(torrent_file.length)
            
            self.file_handles[torrent_file] = file_path
    
    def write_piece_data(self, piece_index: int, data: bytes):
        
        piece_start = piece_index * self.torrent.piece_length
        piece_end = piece_start + len(data)
        
        for torrent_file in self.torrent.files:
            file_start = torrent_file.offset
            file_end = file_start + torrent_file.length
            
          
            if piece_start < file_end and piece_end > file_start:
                
                overlap_start = max(piece_start, file_start)
                overlap_end = min(piece_end, file_end)
                
          
                data_offset = overlap_start - piece_start
                file_offset = overlap_start - file_start
                overlap_length = overlap_end - overlap_start
                
               
                file_path = self.file_handles[torrent_file]
                with open(file_path, 'r+b') as f:
                    f.seek(file_offset)
                    f.write(data[data_offset:data_offset + overlap_length])

class TrackerClient:
    
    def __init__(self, torrent: Torrent, peer_id: bytes):
        self.torrent = torrent
        self.peer_id = peer_id
        
    def get_peers(self) -> List[Tuple[str, int]]:
   
        for tier in self.torrent.announce_list:
            for announce_url in tier:
                print(f"Trying tracker: {announce_url}")
                peers = self._try_tracker(announce_url)
                if peers:
                    print(f"Got {len(peers)} peers from {announce_url}")
                    return peers
                else:
                    print(f"No peers from {announce_url}")
        
        print("No peers available from any tracker")
        return []
    
    def _try_tracker(self, announce_url: str) -> List[Tuple[str, int]]:
  
        if announce_url.startswith('udp://'):
            return self._get_peers_udp(announce_url)
        elif announce_url.startswith(('http://', 'https://')):
            return self._get_peers_http(announce_url)
        else:
            print(f"Unsupported tracker protocol: {announce_url}")
            return []
    
    def _get_peers_http(self, announce_url: str) -> List[Tuple[str, int]]:
        
        params = {
            'info_hash': self.torrent.info_hash,
            'peer_id': self.peer_id,
            'port': 6881,
            'uploaded': 0,
            'downloaded': 0,
            'left': self.torrent.total_length,
            'compact': 1,
            'event': 'started'
        }
        
      
        encoded_params = []
        for key, value in params.items():
            if isinstance(value, bytes):
                encoded_params.append(f"{key}={urllib.parse.quote(value)}")
            else:
                encoded_params.append(f"{key}={value}")
        
        url = f"{announce_url}?{'&'.join(encoded_params)}"
        
        try:
            response = urllib.request.urlopen(url, timeout=10)
            data = bencodepy.decode(response.read())
            
            if b'failure reason' in data:
                print(f"Tracker error: {data[b'failure reason'].decode()}")
                return []
            
            peers_data = data[b'peers']
            return self._parse_peers(peers_data)
            
        except Exception as e:
            print(f"Failed to contact HTTP tracker: {e}")
            return []
    
    def _get_peers_udp(self, announce_url: str) -> List[Tuple[str, int]]:
       
        try:
      
            url_parts = urllib.parse.urlparse(announce_url)
            tracker_host = url_parts.hostname
            tracker_port = url_parts.port or 80
            
        
            sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            sock.settimeout(10)
            
        
            connection_id = 0x41727101980  
            action = 0  
            transaction_id = random.randint(0, 2**32 - 1)
            
            connect_request = struct.pack('>QII', connection_id, action, transaction_id)
            sock.sendto(connect_request, (tracker_host, tracker_port))
            
        
            response, _ = sock.recvfrom(16)
            if len(response) < 16:
                print("Invalid UDP tracker connect response")
                return []
            
            resp_action, resp_transaction_id, connection_id = struct.unpack('>III', response[:12])
            connection_id = struct.unpack('>Q', response[8:16])[0]
            
            if resp_action != 0 or resp_transaction_id != transaction_id:
                print("UDP tracker connect response mismatch")
                return []
            
            
            action = 1 
            transaction_id = random.randint(0, 2**32 - 1)
            
            announce_request = struct.pack(
                '>QII20s20sQQQIIIiH',
                connection_id,
                action,
                transaction_id,
                self.torrent.info_hash,
                self.peer_id,
                0,  
                self.torrent.total_length, 
                0, 
                0,  
                0,  
                random.randint(0, 2**32 - 1),  
                -1,  
                6881 
            )
            
            sock.sendto(announce_request, (tracker_host, tracker_port))
            
            
            response, _ = sock.recvfrom(8192) 
            if len(response) < 20:
                print("Invalid UDP tracker announce response")
                return []
            
            resp_action, resp_transaction_id, interval, leechers, seeders = struct.unpack('>IIIII', response[:20])
            
            if resp_action != 1 or resp_transaction_id != transaction_id:
                print("UDP tracker announce response mismatch")
                return []
            
            
            peers_data = response[20:]
            return self._parse_peers(peers_data)
            
        except Exception as e:
            print(f"Failed to contact UDP tracker: {e}")
            return []
        finally:
            if 'sock' in locals():
                sock.close()
    
    def _parse_peers(self, peers_data: bytes) -> List[Tuple[str, int]]:
       
        peers = []
        
 
        for i in range(0, len(peers_data), 6):
            if i + 6 > len(peers_data):
                break
            ip_bytes = peers_data[i:i+4]
            port_bytes = peers_data[i+4:i+6]
            ip = '.'.join(str(b) for b in ip_bytes)
            port = struct.unpack('>H', port_bytes)[0]
            peers.append((ip, port))
        
        return peers

class TorrentClient:
   
    
    def __init__(self, torrent_path: str, download_dir: str = '.'):
        self.torrent = Torrent(torrent_path)
        self.download_dir = download_dir
        self.peer_id = b'-PC0001-' + os.urandom(12)
        self.piece_manager = PieceManager(self.torrent)
        self.file_manager = FileManager(self.torrent, download_dir)
        self.tracker_client = TrackerClient(self.torrent, self.peer_id)
        self.peers = []
        self.active_peers = []
        self.running = False
        
    def start_download(self):
        
        print(f"Starting download of: {self.torrent.name}")
        print(f"Total size: {self.torrent.total_length} bytes ({self.torrent.total_length / (1024*1024*1024):.2f} GB)")
        print(f"Number of pieces: {self.torrent.num_pieces}")
        print(f"Piece size: {self.torrent.piece_length} bytes ({self.torrent.piece_length / 1024:.0f} KB)")
        print(f"Number of files: {len(self.torrent.files)}")
        print(f"Info hash: {self.torrent.info_hash.hex()}")
        
       
        self.file_manager.create_files()
        
      
        peer_list = self.tracker_client.get_peers()
        if not peer_list:
            print("No peers available")
            return
        
        print(f"Found {len(peer_list)} peers")
        
      
        print("Sample peers:")
        for i, (ip, port) in enumerate(peer_list[:5]):
            print(f"  {ip}:{port}")
        
       
        for ip, port in peer_list:
            peer = Peer(ip, port, self.torrent.info_hash, self.peer_id)
            self.peers.append(peer)
        
        self.running = True
        
        
        print(f"Attempting to connect to peers...")
        attempted_count = 0
        
        for peer in self.peers:
            if attempted_count >= 20: 
                break
            attempted_count += 1
            thread = threading.Thread(target=self._connect_and_handle_peer, args=(peer,))
            thread.daemon = True
            thread.start()
            time.sleep(0.1) 
        
        print(f"Started connection attempts to {attempted_count} peers")
        
       
        time.sleep(3)
        
       
        last_progress = 0
        stall_count = 0
        target_peers = 50
        max_peers = 100
        
        while self.running and not self.piece_manager.is_complete():
            time.sleep(1.1)
            progress = self.piece_manager.get_progress()
            active_peers = sum(1 for peer in self.peers if peer.connected)
            
            print(f"Progress: {progress:.1f}% ({active_peers} active peers)") 
            
        
          
            if active_peers < target_peers and attempted_count < len(self.peers):
                peers_to_add = min(target_peers - active_peers, 10)
                peers_to_add = max(peers_to_add, 0) 
                
                if peers_to_add > 0:
                    print(f"Adding {peers_to_add} more peers...")
                    for i in range(peers_to_add):
                        if attempted_count < len(self.peers):
                            peer = self.peers[attempted_count]
                            if not peer.connected:
                                thread = threading.Thread(target=self._connect_and_handle_peer, args=(peer,))
                                thread.daemon = True
                                thread.start()
                                time.sleep(0.05)
                            attempted_count += 1

            if progress == last_progress:
                stall_count += 1
                if stall_count > 5:
                    target_peers = min(target_peers + 10, max_peers)
                else:
                    stall_count = 0
                    last_progress = progress    
                
        
        if self.piece_manager.is_complete():
            print("Download completed!")
        else:
            print("Download stopped")
        
        self.running = False
    
    def _connect_and_handle_peer(self, peer: Peer):
      
        try:
            if peer.connect():
                print(f"✓ Connected to peer {peer.ip}:{peer.port}")
                self._handle_peer(peer)
        except Exception as e:
            pass  
        finally:
            peer.disconnect()
    
    def _handle_peer(self, peer: Peer):
      
      
        peer.send_interested()
        peer.interested = True
        
        piece_size = 16384 
        current_piece = None
        blocks_requested = set()
        peer_id = f"{peer.ip}:{peer.port}"
        
        try:
          
            peer.socket.settimeout(5)
            
            message_timeout = 0
            while self.running and peer.connected and message_timeout < 15:
                try:
                  
                    message = peer.receive_message()
                    if message is None:
                        message_timeout += 1
                        time.sleep(0.5)
                        continue
                    
                    message_timeout = 0 
                    message_id, payload = message
                    
                    if message_id == -1:  
                        continue
                    elif message_id == 0:  
                        peer.peer_choking = True
                    elif message_id == 1:  
                        peer.peer_choking = False
                        print(f"Peer {peer.ip}:{peer.port} unchoked us")
                    elif message_id == 4:  
                        piece_index = struct.unpack('>I', payload)[0]
                  
                    elif message_id == 5:  
                        peer.bitfield = payload
                        print(f"Received bitfield from {peer.ip}:{peer.port}")
                    elif message_id == 7:  
                    
                        if len(payload) < 8:
                            continue
                        piece_index = struct.unpack('>I', payload[:4])[0]
                        begin = struct.unpack('>I', payload[4:8])[0]
                        data = payload[8:]
                      
                        complete_piece_data = self.piece_manager.add_block(piece_index, begin, data)
                        
                        if complete_piece_data is not None:
                        
                            if self.piece_manager.store_piece(piece_index, complete_piece_data):
                                self.file_manager.write_piece_data(piece_index, complete_piece_data)
                                print(f"Successfully completed piece {piece_index}")
                                
                               
                                if current_piece == piece_index:
                                    current_piece = None
                                    blocks_requested.clear()
                            else:
                        
                                if current_piece == piece_index:
                                    current_piece = None
                                    blocks_requested.clear()
                    
                  
                    if (not peer.peer_choking and current_piece is None):
                        next_piece = self.piece_manager.get_next_piece(peer_id)
                        if next_piece is not None:
                            print(f"Requesting piece {next_piece} from {peer.ip}:{peer.port}")
                            
                      
                            piece_length = self.torrent.piece_length
                            if next_piece == self.torrent.num_pieces - 1:
                                piece_length = self.torrent.total_length - (next_piece * self.torrent.piece_length)
                            
                            current_piece = next_piece
                            blocks_requested.clear()
                            
                          
                            for offset in range(0, piece_length, piece_size):
                                block_size = min(piece_size, piece_length - offset)
                                if peer.request_piece(next_piece, offset, block_size):
                                    blocks_requested.add(offset)
                                else:
                                    print(f"Failed to request block at offset {offset}")
                    
                except socket.timeout:
                    message_timeout += 1
                   
                    peer.send_message(-1)
                    continue
                except Exception as e:
                    print(f"Error in message handling with {peer.ip}:{peer.port}: {e}")
                    break
                
        except Exception as e:
            print(f"Error handling peer {peer.ip}:{peer.port}: {e}")
        finally:
          
            if current_piece is not None:
                self.piece_manager.release_piece(current_piece)
            peer.disconnect()

def main():
    if len(sys.argv) != 2:
        print("Usage: python torrent_client.py <torrent_file>")
        sys.exit(1)
    
    torrent_file = sys.argv[1]
    download_dir = "downloads"
    
    if not os.path.exists(torrent_file):
        print(f"Torrent file not found: {torrent_file}")
        sys.exit(1)
    
    os.makedirs(download_dir, exist_ok=True)
    
    client = TorrentClient(torrent_file, download_dir)
    
    try:
        client.start_download()
    except KeyboardInterrupt:
        print("\nDownload interrupted by user")
        client.running = False

if __name__ == "__main__":
    main()