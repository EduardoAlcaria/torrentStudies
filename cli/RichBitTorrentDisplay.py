from rich.console import Console
from rich.progress import Progress, BarColumn, TextColumn, TimeRemainingColumn, FileSizeColumn, TotalFileSizeColumn, TransferSpeedColumn
from rich.table import Table
from rich.panel import Panel
from rich.layout import Layout
from rich.live import Live
from rich.text import Text






class RichBitTorrentDisplay:
    def __init__(self):
        self.console = Console()
        self.progress = Progress(
            TextColumn("[bold blue]{task.fields[filename]}", justify="left"),
            BarColumn(bar_width=None),
            "[progress.percentage]{task.percentage:>3.1f}%",
            "•",
            FileSizeColumn(),
            "•",
            TotalFileSizeColumn(),
            console=self.console,
            expand=True
        )
        self.task_id = None
        self.peers_data = []
        self.stats = {
            'downloaded': 0,
            'uploaded': 0,
            'download_speed': 0,
            'upload_speed': 0,
            'active_peers': 0,
            'total_peers': 0,
            'pieces_completed': 0,
            'total_pieces': 0,
            'connected_time': 0
        }
        
    def start_download(self, filename, total_size, total_pieces):
        
        self.task_id = self.progress.add_task(
            "download", 
            filename=filename,
            total=total_size
        )
        self.stats['total_pieces'] = total_pieces
        
    def update_progress(self, downloaded_bytes):
       
        if self.task_id is not None:
            self.progress.update(self.task_id, completed=downloaded_bytes)
            self.stats['downloaded'] = downloaded_bytes
    
    def update_peers(self, peers_list):
     
        self.peers_data = []
        active_count = 0
        
        for peer in peers_list:
            if hasattr(peer, 'connected') and peer.connected:
                active_count += 1
                self.peers_data.append({
                    'ip': peer.ip,
                    'port': peer.port,
                    'status': 'Connected' if peer.connected else 'Disconnected',
                    'download_speed': getattr(peer, 'download_speed', 0),
                    'upload_speed': getattr(peer, 'upload_speed', 0),
                    'pieces': getattr(peer, 'pieces_count', 0)
                })
        
        self.stats['active_peers'] = active_count
        self.stats['total_peers'] = len(peers_list)
    
    def update_pieces(self, completed_pieces):
    
        self.stats['pieces_completed'] = completed_pieces
    
    def create_stats_panel(self):
       
        stats_table = Table(show_header=False, box=None, padding=(0, 1))
        stats_table.add_column("Stat", style="cyan", width=15)
        stats_table.add_column("Value", style="white")
        
      
        dl_speed = f"{self.stats['download_speed'] / 1024 / 1024:.2f} MB/s" if self.stats['download_speed'] > 0 else "0 MB/s"
        ul_speed = f"{self.stats['upload_speed'] / 1024 / 1024:.2f} MB/s" if self.stats['upload_speed'] > 0 else "0 MB/s"
        
      
        downloaded_mb = self.stats['downloaded'] / 1024 / 1024
        
        stats_table.add_row("Downloaded:", f"{downloaded_mb:.2f} MB")
        stats_table.add_row("Active Peers:", f"[bold]{self.stats['active_peers']}/{self.stats['total_peers']}[/bold]")
        stats_table.add_row("Pieces:", f"{self.stats['pieces_completed']}/{self.stats['total_pieces']}")
        
      
        if self.stats['active_peers'] > 0:
            connection_status = "🟢 Good" if self.stats['active_peers'] >= 10 else "🟡 Fair" if self.stats['active_peers'] >= 5 else "🔴 Poor"
            stats_table.add_row("Connection:", connection_status)
        
        return Panel(stats_table, title="[bold cyan]Statistics[/bold cyan]", border_style="blue")
    
    def create_peers_panel(self):
     
        if not self.peers_data:
            return Panel("[dim]No active peers[/dim]", title="[bold green]Active Peers[/bold green]", border_style="green")
        
        peers_table = Table(show_header=True, header_style="bold magenta")
        peers_table.add_column("IP:Port", width=21)
        peers_table.add_column("Status", width=12)
        peers_table.add_column("Connected", width=12)
        
       
        for i, peer in enumerate(self.peers_data[:15]):
            status_color = "green" if peer['status'] == "Connected" else "red"
           
            connected_time = getattr(peer, 'connected_time', 'Unknown')
            
            peers_table.add_row(
                f"{peer['ip']}:{peer['port']}",
                f"[{status_color}]{peer['status']}[/{status_color}]",
                f"[dim]{connected_time}[/dim]"
            )
        
        title = f"[bold green]Active Peers ({len(self.peers_data)})[/bold green]"
        return Panel(peers_table, title=title, border_style="green")
    
    def create_layout(self):
        
        layout = Layout()
        
        layout.split(
            Layout(name="progress", size=3),
            Layout(name="main"),
        )
        
        layout["main"].split_row(
            Layout(name="stats", ratio=1),
            Layout(name="peers", ratio=2),
        )
        
        layout["progress"].update(self.progress)
        layout["stats"].update(self.create_stats_panel())
        layout["peers"].update(self.create_peers_panel())
        
        return layout
    
    def display_header(self):
       
        header = Text.assemble(
            ("bold red"),
            ("Custom BitTorrent Client", "bold cyan"),
            ("bold red")
        )
        self.console.print(Panel(header, style="bold blue"))
    
    def log_message(self, message, style="white"):
      
        self.console.print(f"[{style}]• {message}[/{style}]")

