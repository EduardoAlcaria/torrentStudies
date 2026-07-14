import os
from torrent import Torrent



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









