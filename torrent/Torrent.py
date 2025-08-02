import bencodepy
from typing import List, Optional
import hashlib
from dl_torrent import TorrentFile




class Torrent:
    
    def __init__(self, torrent_path: str):
      
        with open(torrent_path, 'rb') as f:
            self.torrent_data = bencodepy.decode(f.read())
    
        self.parse_info()
        
       
        self.parse_files()
        
   
        self.parse_announce_sources()

    def parse_info(self):
        self.info = self.torrent_data[b'info']
        self.info_hash = hashlib.sha1(bencodepy.encode(self.info)).digest()
        self.piece_length = self.info[b'piece length']
        self.pieces = self.info[b'pieces']
        self.num_pieces = len(self.pieces) // 20

    def parse_files(self):
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

    def parse_announce_sources(self):
        
        self.announce: Optional[str] = None
        self.web_seeds: List[str] = []
        self.announce_list: List[List[str]] = []

        for i in self.torrent_data:
            print("\033[1;033m", i ,"\033[m")
        
        if b'announce' in self.torrent_data:
            self.announce = self.torrent_data[b'announce'].decode('utf-8')

       
        if b'url-list' in self.torrent_data:
            for url in self.torrent_data[b'url-list']:
                print("\033[1;033m", url ,"\033[m")     
                self.web_seeds.append(url.decode('utf-8'))
           
            if not self.announce and self.web_seeds:
                self.announce = self.web_seeds[0]

        if b'announce-list' in self.torrent_data:
            for tier in self.torrent_data[b'announce-list']:
                tier_trackers = []
                for tracker in tier:
                    tier_trackers.append(tracker.decode('utf-8'))
                self.announce_list.append(tier_trackers)
        elif self.announce:
            
            self.announce_list = [[self.announce]]

    def get_piece_hash(self, piece_index: int) -> bytes:
        start = piece_index * 20
        return self.pieces[start:start + 20]

    def has_web_seeds(self) -> bool:
        return len(self.web_seeds) > 0

    def has_trackers(self) -> bool:
        return any(
            tracker for tier in self.announce_list 
            for tracker in tier 
            if not tracker.startswith(('http://', 'https://'))
        )

    def get_all_sources(self) -> List[str]:
        sources = []
        for tier in self.announce_list:
            sources.extend(tier)
        return sources

    def __str__(self) -> str:
        return f"Torrent(name='{self.name}', files={len(self.files)}, size={self.total_length})"

    def __repr__(self) -> str:
        return self.__str__()