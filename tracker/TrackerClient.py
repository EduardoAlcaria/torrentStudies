from typing import List, Tuple
import struct
import bencodepy
from torrent import Torrent
from getPeers import get_peers_https, get_peers_udp




class TrackerClient:
    def __init__(self, torrent: Torrent, peer_id: bytes, port: int = 6881):
        self.torrent = torrent
        self.peer_id = peer_id
        self.port = port

        
    def get_peers(self) -> List[Tuple[str, int]]:
        all_peers = []
    
        for tier in self.torrent.announce_list:
            tier_peers = []
        
            for tracker_url in tier:
                print(f"Trying tracker: {tracker_url}")
                peers = self.try_tracker(tracker_url)
                
                if peers:
                    print("\033[01;33m", peers, "\033[;m")
                    tier_peers.extend(peers)
                    print(f"Got {len(peers)} peers from {tracker_url}")
                else:
                    print(f"No peers from {tracker_url}")
            
            if tier_peers:
                all_peers.extend(tier_peers)
                break  
        
        unique_peers = list(set(all_peers))
        if not unique_peers:
            print("No Peers from TrackerClient")
        return unique_peers
    
    def try_tracker(self, announce_url: str) -> List[Tuple[str, int]]:
        print(f"Contacting tracker: {announce_url}")

        if announce_url.startswith('udp://'):
            return get_peers_udp.get_peers_udp(announce_url, self.torrent, self.peer_id, self.port)
        
        elif announce_url.startswith('http://') or announce_url.startswith('https://'):
           
            if not self._is_valid_tracker_url(announce_url):
                print(f"Warning: URL doesn't look like a tracker: {announce_url}")
                return []
            
            http_client = get_peers_https(self.torrent, self.peer_id, self.port)
            return http_client.get_peers_https(announce_url)
        
        else:
            print(f"Unsupported tracker protocol: {announce_url}")
            return []
    
    def _is_valid_tracker_url(self, url: str) -> bool:
       
        if url.endswith('/announce'):
            return True
        
       
        if url.endswith('/') or 'iso' in url or 'mirror' in url or 'pub' in url:
            return False
            
        return True
    
    def parse_peers(self, peers_data: bytes) -> List[Tuple[str, int]]:
        try:
            decoded = bencodepy.decode(peers_data)
                
            if b'failure reason' in decoded:
                print(f"Tracker failure: {decoded[b'failure reason'].decode()}")
                return []
                
            peers_data = decoded.get(b'peers', b'')
            if not peers_data:
                print("No peers in response")
                return []
            
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
        except Exception as e:
            print(f"Error parsing peers: {e}")
            return []
    
    def debug_torrent_trackers(self):
       
        print("=== Torrent Tracker Debug Info ===")
        print(f"Main announce URL: {getattr(self.torrent, 'announce', 'Not found')}")
        
        if hasattr(self.torrent, 'announce_list'):
            print(f"Announce list has {len(self.torrent.announce_list)} tiers:")
            for i, tier in enumerate(self.torrent.announce_list):
                print(f"  Tier {i}: {len(tier)} trackers")
                for j, tracker in enumerate(tier):
                    is_valid = self._is_valid_tracker_url(tracker)
                    status = "✓" if is_valid else "✗"
                    print(f"    {status} {tracker}")
        else:
            print("No announce_list found")
        
        print("=== End Debug Info ===")
    
    def get_valid_trackers(self) -> List[str]:
        
        valid_trackers = []
    
        if hasattr(self.torrent, 'announce') and self._is_valid_tracker_url(self.torrent.announce):
            valid_trackers.append(self.torrent.announce)
     
        if hasattr(self.torrent, 'announce_list'):
            for tier in self.torrent.announce_list:
                for tracker in tier:
                    if self._is_valid_tracker_url(tracker) and tracker not in valid_trackers:
                        valid_trackers.append(tracker)
        
        return valid_trackers
    




    


    