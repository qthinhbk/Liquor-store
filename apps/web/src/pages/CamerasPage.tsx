import CloseIcon from '@mui/icons-material/Close';
import FiberManualRecordIcon from '@mui/icons-material/FiberManualRecord';
import OpenInFullIcon from '@mui/icons-material/OpenInFull';
import RefreshIcon from '@mui/icons-material/Refresh';
import SearchIcon from '@mui/icons-material/Search';
import { Box, Button, Card, CardActionArea, Chip, Dialog, FormControl, IconButton, InputAdornment, InputLabel, MenuItem, Paper, Select, Stack, TextField, ToggleButton, ToggleButtonGroup, Typography } from '@mui/material';
import { useQuery } from '@tanstack/react-query';
import { useGSAP } from '@gsap/react';
import gsap from 'gsap';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useAuth } from '../auth/AuthProvider';
import { EmptyState, InlineError, ScreenLoader } from '../components/common';
import { api, getErrorMessage } from '../lib/api';
import type { Camera } from '../lib/types';

interface LiveFeed {
  camera: Camera;
  imageUrl: string;
  videoUrl: string;
  channelNumber: number;
}

type StatusFilter = 'ALL' | 'LIVE' | 'OFFLINE';
type CameraSort = 'DEFAULT' | 'NAME' | 'AREA' | 'OFFLINE_FIRST';

interface CameraPreferences {
  search: string;
  status: StatusFilter;
  area: string;
  sort: CameraSort;
}

const preferencesKey = 'liquor-store.camera-filters';
const scrollKey = 'liquor-store.camera-scroll';
const defaultPreferences: CameraPreferences = { search: '', status: 'ALL', area: 'ALL', sort: 'DEFAULT' };

function restorePreferences(): CameraPreferences {
  try {
    const stored = sessionStorage.getItem(preferencesKey);
    if (!stored) return defaultPreferences;
    const value = JSON.parse(stored) as Partial<CameraPreferences>;
    return {
      search: typeof value.search === 'string' ? value.search : '',
      status: value.status === 'LIVE' || value.status === 'OFFLINE' ? value.status : 'ALL',
      area: typeof value.area === 'string' ? value.area : 'ALL',
      sort: value.sort === 'NAME' || value.sort === 'AREA' || value.sort === 'OFFLINE_FIRST' ? value.sort : 'DEFAULT',
    };
  } catch {
    return defaultPreferences;
  }
}

function cameraArea(camera: Camera) {
  const value = `${camera.name} ${camera.location}`.toLocaleLowerCase();
  if (value.includes('counter')) return 'Counter';
  if (value.includes('whole store') || value.includes('store top') || value.includes('store corner')) return 'Whole store';
  if (value.includes('kitchen') || value.includes('foodbox')) return 'Kitchen';
  if (value.includes('storage')) return 'Storage';
  if (value.includes('entrance')) return 'Entrance';
  if (value.includes('cold drink')) return 'Cold drinks';
  if (value.includes('control room')) return 'Control room';
  return 'Other';
}

function isCameraOnline(camera: Camera) {
  return camera.isEnabled && camera.status === 'ONLINE';
}

function recordAsset(camera: Camera, extension: 'jpg' | 'mp4') {
  return `/live-cameras/${encodeURIComponent(camera.streamGatewayRef)}.${extension}`;
}

export function CamerasPage() {
  const pageRef = useRef<HTMLDivElement>(null);
  const restoredScrollRef = useRef(false);
  const { store } = useAuth();
  const [preferences, setPreferences] = useState<CameraPreferences>(restorePreferences);
  const [selectedFeedId, setSelectedFeedId] = useState<string | null>(null);

  const {
    data: cameras = [],
    error,
    isLoading,
    isFetching,
    refetch,
  } = useQuery({
    queryKey: ['cameras', store?.id],
    queryFn: () => api.getCameras(store!.id),
    enabled: Boolean(store),
    staleTime: 30_000,
    gcTime: 10 * 60_000,
    refetchInterval: 60_000,
  });

  const feeds = useMemo<LiveFeed[]>(() => cameras
    .filter((camera) => camera.streamGatewayRef.startsWith('record-'))
    .map((camera, index) => ({
      camera,
      imageUrl: recordAsset(camera, 'jpg'),
      videoUrl: recordAsset(camera, 'mp4'),
      channelNumber: index + 1,
    })), [cameras]);

  const onlineCount = feeds.filter(({ camera }) => isCameraOnline(camera)).length;
  const offlineCount = feeds.length - onlineCount;
  const areas = useMemo<string[]>(() => Array.from(new Set<string>(feeds.map(({ camera }) => cameraArea(camera)))).sort((left, right) => left.localeCompare(right)), [feeds]);
  const selectedArea = preferences.area === 'ALL' || areas.includes(preferences.area) ? preferences.area : 'ALL';

  const filteredFeeds = useMemo(() => {
    const search = preferences.search.trim().toLocaleLowerCase();
    const result = feeds.filter(({ camera }) => {
      const online = isCameraOnline(camera);
      if (preferences.status === 'LIVE' && !online) return false;
      if (preferences.status === 'OFFLINE' && online) return false;
      if (selectedArea !== 'ALL' && cameraArea(camera) !== selectedArea) return false;
      if (search && !`${camera.name} ${camera.location} ${cameraArea(camera)}`.toLocaleLowerCase().includes(search)) return false;
      return true;
    });
    return [...result].sort((left, right) => {
      if (preferences.sort === 'NAME') return left.camera.name.localeCompare(right.camera.name);
      if (preferences.sort === 'AREA') return cameraArea(left.camera).localeCompare(cameraArea(right.camera)) || left.camera.name.localeCompare(right.camera.name);
      if (preferences.sort === 'OFFLINE_FIRST') return Number(isCameraOnline(left.camera)) - Number(isCameraOnline(right.camera)) || left.camera.name.localeCompare(right.camera.name);
      return left.channelNumber - right.channelNumber;
    });
  }, [feeds, preferences.search, preferences.sort, preferences.status, selectedArea]);

  const selectedFeed = useMemo(() => feeds.find(({ camera }) => camera.id === selectedFeedId) ?? null, [feeds, selectedFeedId]);
  const selectedFeedOnline = Boolean(selectedFeed?.camera.isEnabled && selectedFeed.camera.status === 'ONLINE');

  useEffect(() => {
    sessionStorage.setItem(preferencesKey, JSON.stringify(preferences));
  }, [preferences]);

  useEffect(() => {
    if (isLoading || restoredScrollRef.current) return;
    restoredScrollRef.current = true;
    const savedPosition = Number(sessionStorage.getItem(scrollKey));
    if (Number.isFinite(savedPosition) && savedPosition > 0) requestAnimationFrame(() => window.scrollTo({ top: savedPosition }));
  }, [isLoading]);

  useEffect(() => {
    const saveScrollPosition = () => sessionStorage.setItem(scrollKey, String(window.scrollY));
    window.addEventListener('scroll', saveScrollPosition, { passive: true });
    return () => window.removeEventListener('scroll', saveScrollPosition);
  }, []);

  useGSAP(() => {
    gsap.from('.live-feed', { y: 14, scale: 0.985, opacity: 0, duration: 0.45, stagger: 0.035, ease: 'power2.out' });
  }, { scope: pageRef, dependencies: [isLoading, preferences.search, preferences.status, selectedArea, preferences.sort], revertOnUpdate: true });

  if (!store || isLoading) return <ScreenLoader label="Loading cameras…" />;

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

      {error && <InlineError message={getErrorMessage(error)} />}

      <Paper
        variant="outlined"
        sx={{
          position: 'sticky',
          top: { xs: 66, md: 12 },
          zIndex: 5,
          p: { xs: 1.5, sm: 2 },
          bgcolor: 'rgba(13,16,16,.96)',
          backdropFilter: 'blur(16px)',
          borderColor: '#2A302E',
        }}
      >
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr 1fr', md: 'minmax(260px, 1.5fr) minmax(160px, .7fr) minmax(170px, .75fr) auto' }, gap: 1.25 }}>
          <TextField
            size="small"
            value={preferences.search}
            onChange={(event) => setPreferences((current) => ({ ...current, search: event.target.value }))}
            placeholder="Search cameras"
            aria-label="Search cameras"
            sx={{ gridColumn: { xs: '1 / -1', md: 'auto' } }}
            InputProps={{ startAdornment: <InputAdornment position="start"><SearchIcon fontSize="small" /></InputAdornment> }}
          />
          <FormControl size="small">
            <InputLabel id="camera-area-label">Area</InputLabel>
            <Select
              labelId="camera-area-label"
              label="Area"
              value={selectedArea}
              onChange={(event) => setPreferences((current) => ({ ...current, area: event.target.value }))}
            >
              <MenuItem value="ALL">All areas</MenuItem>
              {areas.map((area) => <MenuItem key={area} value={area}>{area}</MenuItem>)}
            </Select>
          </FormControl>
          <FormControl size="small">
            <InputLabel id="camera-sort-label">Sort</InputLabel>
            <Select
              labelId="camera-sort-label"
              label="Sort"
              value={preferences.sort}
              onChange={(event) => setPreferences((current) => ({ ...current, sort: event.target.value as CameraSort }))}
            >
              <MenuItem value="DEFAULT">Camera order</MenuItem>
              <MenuItem value="NAME">Name A–Z</MenuItem>
              <MenuItem value="AREA">Area</MenuItem>
              <MenuItem value="OFFLINE_FIRST">Offline first</MenuItem>
            </Select>
          </FormControl>
          <Button
            variant="outlined"
            startIcon={<RefreshIcon />}
            disabled={isFetching}
            onClick={() => void refetch()}
            sx={{ gridColumn: { xs: '1 / -1', md: 'auto' }, whiteSpace: 'nowrap' }}
          >
            {isFetching ? 'Updating' : 'Refresh'}
          </Button>
        </Box>

        <Box sx={{ mt: 1.5, display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between', gap: 1.25 }}>
          <ToggleButtonGroup
            exclusive
            size="small"
            value={preferences.status}
            onChange={(_, value: StatusFilter | null) => value && setPreferences((current) => ({ ...current, status: value }))}
            aria-label="Camera status"
          >
            <ToggleButton value="ALL">All {feeds.length}</ToggleButton>
            <ToggleButton value="LIVE">Live {onlineCount}</ToggleButton>
            <ToggleButton value="OFFLINE">Offline {offlineCount}</ToggleButton>
          </ToggleButtonGroup>
          <Typography variant="caption" color="text.secondary">Showing {filteredFeeds.length} of {feeds.length} cameras</Typography>
        </Box>
      </Paper>

      {feeds.length === 0 ? <EmptyState title="No record feeds found" description="Run the demo seed to load camera records." /> : filteredFeeds.length === 0 ? (
        <Paper variant="outlined" sx={{ py: 6, px: 3, textAlign: 'center', borderStyle: 'dashed' }}>
          <Typography variant="h6">No cameras match these filters</Typography>
          <Typography color="text.secondary" sx={{ mt: 0.75 }}>Try another search or clear the current camera filters.</Typography>
          <Button variant="outlined" sx={{ mt: 2 }} onClick={() => setPreferences(defaultPreferences)}>Clear filters</Button>
        </Paper>
      ) : (
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, minmax(0, 1fr))', lg: 'repeat(3, minmax(0, 1fr))', xl: 'repeat(4, minmax(0, 1fr))' }, gridAutoFlow: 'dense', gap: 1.25 }}>
          {filteredFeeds.map((feed) => {
            const online = isCameraOnline(feed.camera);
            return (
              <Card key={feed.camera.id} className="live-feed" sx={{ overflow: 'hidden', '&:hover': { borderColor: '#6B5435', transform: 'translateY(-2px)' }, '&:hover .camera-thumbnail': { transform: 'scale(1.045)' }, transition: 'transform 240ms ease, border-color 240ms ease' }}>
                <CardActionArea onClick={() => setSelectedFeedId(feed.camera.id)} aria-label={`Open live view for ${feed.camera.name}`}>
                  <Box sx={{ position: 'relative', aspectRatio: '16 / 9', bgcolor: '#050606', overflow: 'hidden' }}>
                    <Box className="camera-thumbnail" component="img" src={feed.imageUrl} alt={`${feed.camera.name} camera preview`} sx={{ width: '100%', height: '100%', objectFit: 'cover', filter: online ? 'brightness(.86) contrast(1.1)' : 'grayscale(1) brightness(.42)', transition: 'transform 700ms ease' }} />
                    <Box sx={{ position: 'absolute', top: 10, left: 10, display: 'flex', alignItems: 'center', gap: 0.6, bgcolor: 'rgba(9,11,12,.82)', color: online ? '#9BD2AC' : '#C6C9C7', px: 1, py: 0.45, borderRadius: 0.5, fontSize: 10, fontWeight: 750, letterSpacing: '.08em' }}>
                      <FiberManualRecordIcon sx={{ fontSize: 9 }} />
                      {online ? 'LIVE' : 'OFFLINE'}
                    </Box>
                    <Box sx={{ position: 'absolute', right: 10, bottom: 9, display: 'flex', alignItems: 'center', gap: 0.65, px: 0.8, py: 0.35, bgcolor: 'rgba(9,11,12,.78)', color: '#D5D8D6', fontSize: 10, fontFamily: 'monospace' }}>
                      <OpenInFullIcon sx={{ fontSize: 12 }} />
                      CAM {String(feed.channelNumber).padStart(2, '0')}
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
        onClose={() => setSelectedFeedId(null)}
        maxWidth="lg"
        fullWidth
        PaperProps={{ sx: { overflow: 'hidden', bgcolor: '#050606', backgroundImage: 'none', border: '1px solid #303735' } }}
      >
        {selectedFeed && (
          <Box sx={{ position: 'relative', bgcolor: '#020303', aspectRatio: '16 / 9', maxHeight: 'calc(100vh - 80px)' }}>
            {selectedFeedOnline ? (
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
            ) : (
              <Box
                component="img"
                src={selectedFeed.imageUrl}
                alt={`${selectedFeed.camera.name} offline camera image`}
                sx={{ display: 'block', width: '100%', height: '100%', objectFit: 'contain', bgcolor: '#000', filter: 'grayscale(1) brightness(.42)' }}
              />
            )}
            <Box sx={{ position: 'absolute', inset: 0, pointerEvents: 'none', background: 'linear-gradient(180deg, rgba(0,0,0,.52), transparent 22%, transparent 72%, rgba(0,0,0,.68))' }} />
            <Box sx={{ position: 'absolute', top: 16, left: 18, display: 'flex', alignItems: 'center', gap: 0.8, color: selectedFeedOnline ? '#A8DAB7' : '#C6C9C7', fontSize: 11, fontWeight: 800, letterSpacing: '.12em' }}>
              <FiberManualRecordIcon sx={{ fontSize: 10 }} /> {selectedFeedOnline ? 'LIVE PREVIEW' : 'CAMERA OFFLINE'}
            </Box>
            <IconButton onClick={() => setSelectedFeedId(null)} aria-label="Close live view" sx={{ position: 'absolute', top: 9, right: 10, color: '#FFF', bgcolor: 'rgba(0,0,0,.5)', '&:hover': { bgcolor: 'rgba(0,0,0,.78)' } }}>
              <CloseIcon />
            </IconButton>
            <Box sx={{ position: 'absolute', left: 18, right: 18, bottom: 15, display: 'flex', justifyContent: 'space-between', alignItems: 'end', gap: 2, pointerEvents: 'none' }}>
              <Box>
                <Typography sx={{ color: '#FFF', fontWeight: 700, fontSize: { xs: 15, md: 20 } }}>{selectedFeed.camera.name}</Typography>
                <Typography sx={{ color: 'rgba(255,255,255,.68)', fontSize: 12, mt: 0.25 }}>{selectedFeed.camera.location}</Typography>
              </Box>
              <Typography sx={{ color: 'rgba(255,255,255,.58)', fontFamily: 'monospace', fontSize: 10 }}>{selectedFeedOnline ? 'LOOPED RECORD FEED' : 'STATIC CAMERA IMAGE'}</Typography>
            </Box>
          </Box>
        )}
      </Dialog>
    </Stack>
  );
}
