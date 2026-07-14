import hashlib
import threading
from typing import Optional
import threading
from torrent import Torrent

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

