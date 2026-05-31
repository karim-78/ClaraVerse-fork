import { useState, useEffect, useCallback } from 'react';
import { Smartphone, MoreVertical, Edit2, Trash2, RefreshCw, ExternalLink } from 'lucide-react';
import {
  listDevices,
  renameDevice,
  revokeDevice,
  getPlatformName,
  getPlatformEmoji,
  formatRelativeTime,
  type DeviceInfo,
} from '@/services/deviceService';
import { toast } from '@/store/useToastStore';
import styles from './DevicesSection.module.css';

export const DevicesSection = () => {
  const [devices, setDevices] = useState<DeviceInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeMenu, setActiveMenu] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null);
  const [revoking, setRevoking] = useState<string | null>(null);

  const fetchDevices = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await listDevices();
      setDevices(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to load devices';
      setError(message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchDevices();
  }, [fetchDevices]);

  const handleRename = async () => {
    if (!renaming) return;

    try {
      await renameDevice(renaming.id, renaming.name);
      toast.success('Device renamed successfully');
      setRenaming(null);
      fetchDevices();
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to rename device';
      toast.error(message);
    }
  };

  const handleRevoke = async (deviceId: string) => {
    setRevoking(deviceId);
    try {
      await revokeDevice(deviceId);
      toast.success('Device revoked successfully');
      fetchDevices();
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to revoke device';
      toast.error(message);
    } finally {
      setRevoking(null);
    }
  };

  const openRenameModal = (device: DeviceInfo) => {
    setRenaming({ id: device.device_id, name: device.name });
    setActiveMenu(null);
  };

  const confirmRevoke = (device: DeviceInfo) => {
    setActiveMenu(null);
    if (device.is_current) {
      if (confirm('This will log you out of this device. Are you sure?')) {
        handleRevoke(device.device_id);
      }
    } else {
      if (confirm(`Revoke access for "${device.name}"?`)) {
        handleRevoke(device.device_id);
      }
    }
  };

  if (loading) {
    return (
      <div className={styles.container}>
        <div className={styles.header}>
          <h2 className={styles.title}>
            <Smartphone className={styles.titleIcon} />
            Connected Devices
          </h2>
        </div>
        <div className={styles.loading}>
          <RefreshCw className={styles.spinner} />
          <span>Loading devices...</span>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className={styles.container}>
        <div className={styles.header}>
          <h2 className={styles.title}>
            <Smartphone className={styles.titleIcon} />
            Connected Devices
          </h2>
        </div>
        <div className={styles.error}>
          <p>{error}</p>
          <button onClick={fetchDevices} className={styles.retryButton}>
            <RefreshCw size={16} />
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <div>
          <h2 className={styles.title}>
            <Smartphone className={styles.titleIcon} />
            Connected Devices
          </h2>
          <p className={styles.description}>
            Manage CLI and API devices connected to your account.
          </p>
        </div>
        <button onClick={fetchDevices} className={styles.refreshButton} title="Refresh">
          <RefreshCw size={16} />
        </button>
      </div>

      {devices.length === 0 ? (
        <div className={styles.empty}>
          <Smartphone className={styles.emptyIcon} />
          <h3>No devices connected</h3>
          <p>Use the MCP CLI to connect a device to your account.</p>
          <a
            href="https://docs.claraverse.ai/cli"
            target="_blank"
            rel="noopener noreferrer"
            className={styles.docsLink}
          >
            <ExternalLink size={14} />
            View documentation
          </a>
        </div>
      ) : (
        <div className={styles.deviceList}>
          {devices.map(device => (
            <div
              key={device.device_id}
              className={`${styles.deviceCard} ${device.is_current ? styles.current : ''} ${!device.is_active ? styles.inactive : ''}`}
            >
              <div className={styles.deviceMain}>
                <span className={styles.platformEmoji}>{getPlatformEmoji(device.platform)}</span>
                <div className={styles.deviceInfo}>
                  <div className={styles.deviceName}>
                    {device.name}
                    {device.is_current && <span className={styles.currentBadge}>This Device</span>}
                    {!device.is_active && <span className={styles.inactiveBadge}>Revoked</span>}
                  </div>
                  <div className={styles.deviceMeta}>
                    <span>{getPlatformName(device.platform)}</span>
                    <span className={styles.separator}>&bull;</span>
                    <span>v{device.client_version}</span>
                  </div>
                  <div className={styles.deviceActivity}>
                    <span>Last active: {formatRelativeTime(device.last_active_at)}</span>
                    {device.last_location && (
                      <>
                        <span className={styles.separator}>&bull;</span>
                        <span>{device.last_location}</span>
                      </>
                    )}
                  </div>
                </div>
              </div>

              <div className={styles.deviceActions}>
                <button
                  className={styles.menuButton}
                  onClick={() =>
                    setActiveMenu(activeMenu === device.device_id ? null : device.device_id)
                  }
                  aria-label="Device options"
                >
                  <MoreVertical size={18} />
                </button>

                {activeMenu === device.device_id && (
                  <div className={styles.dropdown}>
                    <button className={styles.dropdownItem} onClick={() => openRenameModal(device)}>
                      <Edit2 size={14} />
                      Rename
                    </button>
                    <button
                      className={`${styles.dropdownItem} ${styles.danger}`}
                      onClick={() => confirmRevoke(device)}
                      disabled={revoking === device.device_id}
                    >
                      <Trash2 size={14} />
                      {revoking === device.device_id ? 'Revoking...' : 'Revoke'}
                    </button>
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Rename Modal */}
      {renaming && (
        <div className={styles.modalBackdrop} onClick={() => setRenaming(null)}>
          <div className={styles.modal} onClick={e => e.stopPropagation()}>
            <h3>Rename Device</h3>
            <input
              type="text"
              value={renaming.name}
              onChange={e => setRenaming({ ...renaming, name: e.target.value })}
              maxLength={50}
              autoFocus
              className={styles.renameInput}
            />
            <div className={styles.modalActions}>
              <button className={styles.cancelButton} onClick={() => setRenaming(null)}>
                Cancel
              </button>
              <button
                className={styles.saveButton}
                onClick={handleRename}
                disabled={!renaming.name.trim()}
              >
                Save
              </button>
            </div>
          </div>
        </div>
      )}

      <div className={styles.footer}>
        <p className={styles.footerNote}>
          Revoking a device will immediately disconnect it and require re-authentication.
        </p>
      </div>
    </div>
  );
};
