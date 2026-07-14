import requests
import bencodepy
import struct
from typing import List, Tuple
import urllib.parse


class get_peers_https:
    def __init__(self, torrent, peer_id: bytes, port: int = 6881, timeout: int = 10):
        self.torrent = torrent
        self.peer_id = peer_id
        self.port = port
        self.timeout = timeout
        

    def get_peers_https(self, announce_url: str) -> List[Tuple[str, int]]:
       
        if not self._is_valid_tracker_url(announce_url):
            print(f"Warning: URL doesn't look like a tracker: {announce_url}")
            return []
            
        info_hash_encoded = urllib.parse.quote(self.torrent.info_hash, safe='')
        
        params = {
            'info_hash': info_hash_encoded,
            'peer_id': self.peer_id.decode('latin-1') if isinstance(self.peer_id, bytes) else self.peer_id,
            'port': self.port,
            'uploaded': 0,
            'downloaded': 0,
            'left': self.torrent.total_length,
            'compact': 1,
            'event': 'started'
        }
        
        try:
            print(f"Contacting HTTP tracker: {announce_url}")
            
            
            headers = {
                'User-Agent': 'BitTorrent/7.10.5',
                'Accept': 'text/plain'
            }
            
            response = requests.get(announce_url, params=params, timeout=self.timeout, headers=headers)
            
            print(f"Response status: {response.status_code}")
            print(f"Response content type: {response.headers.get('content-type', 'unknown')}")
            
            if response.status_code == 200:
                
                if response.content.startswith(b'<'):
                    print("Error: Tracker returned HTML instead of bencoded data")
                    print("This might be a web directory, not a BitTorrent tracker")
                    print(f"Response preview: {response.content[:200]}...")
                    return []
                
                
                if response.content.startswith(b'{'):
                    print("Error: Tracker returned JSON instead of bencoded data")
                    print(f"Response: {response.content.decode('utf-8', errors='ignore')}")
                    return []
                
                return self.parse_tracker_response(response.content)
            else:
                print(f"Tracker returned status {response.status_code}")
                if response.content:
                    print(f"Response content: {response.content[:200]}...")
                return []
                
        except requests.exceptions.Timeout:
            print(f"Timeout contacting tracker {announce_url}")
            return []
        except requests.exceptions.ConnectionError:
            print(f"Connection error contacting tracker {announce_url}")
            return []
        except requests.exceptions.RequestException as e:
            print(f"Request error contacting tracker {announce_url}: {e}")
            return []
        except Exception as e:
            print(f"Unexpected error contacting tracker {announce_url}: {e}")
            return []

    def _is_valid_tracker_url(self, url: str) -> bool:
       
        
        if url.endswith('/announce'):
            return True
        

        
        
        if url.endswith('/') or 'iso' in url or 'mirror' in url or 'pub' in url:
            return False
            
        return True

    def parse_tracker_response(self, response_data: bytes) -> List[Tuple[str, int]]:
        try:
            # First, let's see what we're trying to decode
            print(f"Response data length: {len(response_data)} bytes")
            print(f"Response data starts with: {response_data[:50]}")
            
            decoded = bencodepy.decode(response_data)
            
            print("\033[1;33m", "Decoded response:", decoded, "\033[m")
                
            if b'failure reason' in decoded:
                print(f"Tracker failure: {decoded[b'failure reason'].decode()}")
                return []
                
            peers_data = decoded.get(b'peers', b'')
            if not peers_data:
                print("No peers in response")
                # Check if there's a peers list instead of compact format
                if b'peers' in decoded and isinstance(decoded[b'peers'], list):
                    print("Found peers in list format")
                    return self._parse_peers_list(decoded[b'peers'])
                return []
            
            peers = []
            
            print("\033[1;33m", "Peers data:", peers_data, "\033[m")
            
            # Parse compact peers format
            for i in range(0, len(peers_data), 6):
                if i + 6 > len(peers_data):
                    break
                ip_bytes = peers_data[i:i+4]
                port_bytes = peers_data[i+4:i+6]
                ip = '.'.join(str(b) for b in ip_bytes)
                port = struct.unpack('>H', port_bytes)[0]
                peers.append((ip, port))
            
            print(f"Parsed {len(peers)} peers from compact format")
            return peers
            
        except bencodepy.exceptions.DecodingError as e:
            print(f"Bencode decoding error: {e}")
            print("This is likely not a valid tracker response")
            print(f"Response data: {response_data[:100]}...")
            return []
        except Exception as e:
            print(f"Error parsing tracker response: {e}")
            return []

    def _parse_peers_list(self, peers_list: list) -> List[Tuple[str, int]]:
        """Parse peers from list format (non-compact)"""
        peers = []
        try:
            for peer_dict in peers_list:
                if isinstance(peer_dict, dict):
                    ip = peer_dict.get(b'ip', b'').decode('utf-8')
                    port = peer_dict.get(b'port', 0)
                    if ip and port:
                        peers.append((ip, port))
            
            print(f"Parsed {len(peers)} peers from list format")
            return peers
        except Exception as e:
            print(f"Error parsing peers list: {e}")
            return []

    def debug_request(self, announce_url: str) -> dict:
        """Debug method to inspect the request being made"""
        info_hash_encoded = urllib.parse.quote(self.torrent.info_hash, safe='')
        
        params = {
            'info_hash': info_hash_encoded,
            'peer_id': self.peer_id.decode('latin-1') if isinstance(self.peer_id, bytes) else self.peer_id,
            'port': self.port,
            'uploaded': 0,
            'downloaded': 0,
            'left': self.torrent.total_length,
            'compact': 1,
            'event': 'started'
        }
        
        full_url = f"{announce_url}?{urllib.parse.urlencode(params)}"
        
        return {
            'url': announce_url,
            'params': params,
            'full_url': full_url,
            'info_hash_hex': self.torrent.info_hash.hex(),
            'peer_id_hex': self.peer_id.hex() if isinstance(self.peer_id, bytes) else self.peer_id
        }