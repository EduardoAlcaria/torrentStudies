import socket
import struct
from typing import Optional, Tuple



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





