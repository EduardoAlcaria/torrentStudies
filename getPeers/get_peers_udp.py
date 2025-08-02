import socket
import struct
import random 
import urllib.parse
from typing import List
from typing import Tuple






class get_peers_udp:
    def __init__(self, torrent, peer_id):
        self.torrent = torrent
        self.peer_id = peer_id

    def get_peers_udp(self, announce_url: str) -> List[Tuple[str, int]]:
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
            ip = socket.inet_ntoa(peers_data[i:i + 4])
            port = struct.unpack('>H', peers_data[i + 4:i + 6])[0]
            peers.append((ip, port))
        return peers
    



