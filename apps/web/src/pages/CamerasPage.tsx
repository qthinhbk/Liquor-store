import CloseIcon from '@mui/icons-material/Close';
import FiberManualRecordIcon from '@mui/icons-material/FiberManualRecord';
import OpenInFullIcon from '@mui/icons-material/OpenInFull';
import { Box, Card, CardActionArea, Chip, Dialog, IconButton, Stack, Typography } from '@mui/material';
import { useGSAP } from '@gsap/react';
import gsap from 'gsap';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useAuth } from '../auth/AuthProvider';
import { EmptyState, InlineError, ScreenLoader } from '../components/common';
import { api, getErrorMessage } from '../lib/api';
import type { Camera } from '../lib/types';

interface LiveFeed {
  camera: Camera;
  imageUrl: string;
  videoUrl: string;
}

function recordAsset(camera: Camera, extension: 'jpg' | 'mp4') {
  return `/live-cameras/${encodeURIComponent(camera.streamGatewayRef)}.${extension}`;
}

export function CamerasPage() {
  const pageRef = useRef<HTMLDivElement>(null);
  const { store } = useAuth();
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [selectedFeed, setSelectedFeed] = useState<LiveFeed | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!store) return;
    setLoading(true);
    setError(null);
    try {
      setCameras(await api.getCameras(store.id));
    } catch (cause) {
      setError(getErrorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [store]);

  useEffect(() => { void load(); }, [load]);

  const feeds = useMemo<LiveFeed[]>(() => cameras
    .filter((camera) => camera.streamGatewayRef.startsWith('record-'))
    .map((camera) => ({
      camera,
      imageUrl: recordAsset(camera, 'jpg'),
      videoUrl: recordAsset(camera, 'mp4'),
    })), [cameras]);

  const onlineCount = feeds.filter(({ camera }) => camera.isEnabled && camera.status === 'ONLINE').length;

  useGSAP(() => {
    gsap.from('.live-feed', { y: 14, scale: 0.985, opacity: 0, duration: 0.45, stagger: 0.035, ease: 'power2.out' });
  }, { scope: pageRef, dependencies: [loading] });

  if (loading) return <ScreenLoader label="Loading cameras…" />;

  return (
    <Stack ref={pageRef} spacing={3} sx={{ overflowX: 'hidden' }}>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'end', gap: 2 }}>
        <Box sx={{ maxWidth: 760 }}>
          <Typography component="h1" variant="h4">Live cameras</Typography>
          <Typography color="text.secondary" sx={{ mt: 0.75 }}>
            Select a camera to open its continuously looping deployment-preview feed.
          </Typography>
        </Box>
        <Chip
          color={onlineCount === feeds.length && feeds.length > 0 ? 'success' : 'warning'}
          label={`${onlineCount} of ${feeds.length} live`}
        />
      </Box>

      {error && <InlineError message={error} />}

      {feeds.length === 0 ? <EmptyState title="No record feeds found" description="Run the demo seed to load camera records." /> : (
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, minmax(0, 1fr))', lg: 'repeat(3, minmax(0, 1fr))', xl: 'repeat(4, minmax(0, 1fr))' }, gridAutoFlow: 'dense', gap: 1.25 }}>
          {feeds.map((feed, index) => {
            const online = feed.camera.isEnabled && feed.camera.status === 'ONLINE';
            return (
              <Card key={feed.camera.id} className="live-feed" sx={{ overflow: 'hidden', '&:hover': { borderColor: '#6B5435', transform: 'translateY(-2px)' }, '&:hover .camera-thumbnail': { transform: 'scale(1.045)' }, transition: 'transform 240ms ease, border-color 240ms ease' }}>
                <CardActionArea onClick={() => setSelectedFeed(feed)} aria-label={`Open live view for ${feed.camera.name}`}>
                  <Box sx={{ position: 'relative', aspectRatio: '16 / 9', bgcolor: '#050606', overflow: 'hidden' }}>
                    <Box className="camera-thumbnail" component="img" src={feed.imageUrl} alt={`${feed.camera.name} camera preview`} sx={{ width: '100%', height: '100%', objectFit: 'cover', filter: online ? 'brightness(.86) contrast(1.1)' : 'grayscale(1) brightness(.42)', transition: 'transform 700ms ease' }} />
                    <Box sx={{ position: 'absolute', top: 10, left: 10, display: 'flex', alignItems: 'center', gap: 0.6, bgcolor: 'rgba(9,11,12,.82)', color: online ? '#9BD2AC' : '#C6C9C7', px: 1, py: 0.45, borderRadius: 0.5, fontSize: 10, fontWeight: 750, letterSpacing: '.08em' }}>
                      <FiberManualRecordIcon sx={{ fontSize: 9 }} />
                      {online ? 'LIVE' : 'OFFLINE'}
                    </Box>
                    <Box sx={{ position: 'absolute', right: 10, bottom: 9, display: 'flex', alignItems: 'center', gap: 0.65, px: 0.8, py: 0.35, bgcolor: 'rgba(9,11,12,.78)', color: '#D5D8D6', fontSize: 10, fontFamily: 'monospace' }}>
                      <OpenInFullIcon sx={{ fontSize: 12 }} />
                      CAM {String(index + 1).padStart(2, '0')}
                    </Box>
                  </Box>
                  <Box sx={{ px: 1.75, py: 1.4, textAlign: 'left' }}>
                    <Typography fontWeight={650} noWrap>{feed.camera.name}</Typography>
                    <Typography variant="caption" color="text.secondary" noWrap display="block" sx={{ mt: 0.25 }}>{feed.camera.location}</Typography>
                  </Box>
                </CardActionArea>
              </Card>
            );
          })}
        </Box>
      )}

      <Dialog
        open={Boolean(selectedFeed)}
        onClose={() => setSelectedFeed(null)}
        maxWidth="lg"
        fullWidth
        PaperProps={{ sx: { overflow: 'hidden', bgcolor: '#050606', backgroundImage: 'none', border: '1px solid #303735' } }}
      >
        {selectedFeed && (
          <Box sx={{ position: 'relative', bgcolor: '#020303', aspectRatio: '16 / 9', maxHeight: 'calc(100vh - 80px)' }}>
            <Box
              component="video"
              key={selectedFeed.camera.id}
              src={selectedFeed.videoUrl}
              poster={selectedFeed.imageUrl}
              autoPlay
              muted
              loop
              playsInline
              controls={false}
              disablePictureInPicture
              onContextMenu={(event) => event.preventDefault()}
              sx={{ display: 'block', width: '100%', height: '100%', objectFit: 'contain', bgcolor: '#000' }}
            />
            <Box sx={{ position: 'absolute', inset: 0, pointerEvents: 'none', background: 'linear-gradient(180deg, rgba(0,0,0,.52), transparent 22%, transparent 72%, rgba(0,0,0,.68))' }} />
            <Box sx={{ position: 'absolute', top: 16, left: 18, display: 'flex', alignItems: 'center', gap: 0.8, color: '#A8DAB7', fontSize: 11, fontWeight: 800, letterSpacing: '.12em' }}>
              <FiberManualRecordIcon sx={{ fontSize: 10 }} /> LIVE PREVIEW
            </Box>
            <IconButton onClick={() => setSelectedFeed(null)} aria-label="Close live view" sx={{ position: 'absolute', top: 9, right: 10, color: '#FFF', bgcolor: 'rgba(0,0,0,.5)', '&:hover': { bgcolor: 'rgba(0,0,0,.78)' } }}>
              <CloseIcon />
            </IconButton>
            <Box sx={{ position: 'absolute', left: 18, right: 18, bottom: 15, display: 'flex', justifyContent: 'space-between', alignItems: 'end', gap: 2, pointerEvents: 'none' }}>
              <Box>
                <Typography sx={{ color: '#FFF', fontWeight: 700, fontSize: { xs: 15, md: 20 } }}>{selectedFeed.camera.name}</Typography>
                <Typography sx={{ color: 'rgba(255,255,255,.68)', fontSize: 12, mt: 0.25 }}>{selectedFeed.camera.location}</Typography>
              </Box>
              <Typography sx={{ color: 'rgba(255,255,255,.58)', fontFamily: 'monospace', fontSize: 10 }}>LOOPED RECORD FEED</Typography>
            </Box>
          </Box>
        )}
      </Dialog>
    </Stack>
  );
}
