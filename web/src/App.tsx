import React, { useState, useEffect, useCallback } from 'react';
import { 
  Building2, 
  Database, 
  ToggleLeft, 
  ToggleRight, 
  Play, 
  RefreshCw, 
  CheckCircle2, 
  AlertCircle, 
  ArrowUpCircle, 
  PlusCircle, 
  Server,
  Terminal,
  ShieldCheck,
  UserCheck,
  Users,
  LogOut,
  Trash2,
  Key,
  Lock,
  User
} from 'lucide-react';

const API_BASE = 'http://localhost:8080';

interface CPUser {
  id: string;
  username: string;
  full_name: string;
  email: string;
  role: string;
  created_at: string;
}

interface TenantModule {
  tenant_id: string;
  connection_string: string;
  features: Record<string, boolean>;
  settings: Record<string, string>;
}

interface TenantSetting {
  name: string;
  plan: string;
  global_flags: Record<string, boolean>;
  modules: Record<string, TenantModule>;
}

interface MigrationStatus {
  tenant_id: string;
  module_name: string;
  connection_string: string;
  current_version: number;
  latest_version: number;
  pending_count: number;
  status: string;
  applied_migrations: Array<{ version: number; name: string; applied_at: string; execution_ms: number }>;
  pending_migrations: string[] | null;
  error_message?: string;
}

interface Overview {
  total_tenants: number;
  total_databases: number;
  pending_migrations_dbs: number;
  system_health: string;
}

export default function App() {
  // Authentication State
  const [token, setToken] = useState<string>(() => localStorage.getItem('cp_token') || '');
  const [currentUser, setCurrentUser] = useState<CPUser | null>(null);
  const [authChecking, setAuthChecking] = useState(true);

  // Login Form State
  const [loginUsername, setLoginUsername] = useState('admin');
  const [loginPassword, setLoginPassword] = useState('admin123');
  const [loginLoading, setLoginLoading] = useState(false);
  const [loginError, setLoginError] = useState<string | null>(null);

  // Dashboard Navigation State
  const [activeTab, setActiveTab] = useState<'tenants' | 'users' | 'features' | 'migrations' | 'playground'>('tenants');
  const [overview, setOverview] = useState<Overview | null>(null);
  const [tenants, setTenants] = useState<Record<string, TenantSetting>>({});
  const [migrations, setMigrations] = useState<MigrationStatus[]>([]);
  const [cpUsers, setCpUsers] = useState<CPUser[]>([]);
  const [loading, setLoading] = useState(false);
  const [notification, setNotification] = useState<{ type: 'success' | 'error'; message: string } | null>(null);

  // Form tạo Tenant mới
  const [newTenantID, setNewTenantID] = useState('');
  const [newTenantName, setNewTenantName] = useState('');
  const [newTenantPlan, setNewTenantPlan] = useState('standard');
  const [newAdminUsername, setNewAdminUsername] = useState('admin');
  const [newAdminPassword, setNewAdminPassword] = useState('admin123');
  const [creatingTenant, setCreatingTenant] = useState(false);

  // Form tạo User CP mới
  const [newCpUsername, setNewCpUsername] = useState('');
  const [newCpPassword, setNewCpPassword] = useState('');
  const [newCpFullName, setNewCpFullName] = useState('');
  const [newCpEmail, setNewCpEmail] = useState('');
  const [newCpRole, setNewCpRole] = useState('OPERATOR');
  const [creatingUser, setCreatingUser] = useState(false);

  // Playground state
  const [playTenant, setPlayTenant] = useState('');
  // JWT của từng Tenant nhận được từ POST /api/v1/auth/login trong Playground
  const [playTokens, setPlayTokens] = useState<Record<string, string>>({});
  const [playOutput, setPlayOutput] = useState<string>('Chọn Tenant và bấm nút kiểm thử để quan sát phản hồi thực tế từ Micro-Kernel...');
  const [authUsername, setAuthUsername] = useState('admin');
  const [authPassword, setAuthPassword] = useState('admin123');
  const [authMFACode, setAuthMFACode] = useState('');
  const [partyFullName, setPartyFullName] = useState('Nguyễn Văn Khách Hàng');
  const [partyPhone, setPartyPhone] = useState('0988777666');
  const [partyEmail, setPartyEmail] = useState('client@example.com');
  const [partyIDNumber, setPartyIDNumber] = useState('079090001122');
  const [partyIDType, setPartyIDType] = useState('CCCD');
  const [queryIDNumber, setQueryIDNumber] = useState('079090001122');

  const showNotify = (type: 'success' | 'error', message: string) => {
    setNotification({ type, message });
    setTimeout(() => setNotification(null), 5000);
  };

  const getAuthHeaders = useCallback(() => {
    return {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`
    };
  }, [token]);

  // Kiểm tra phiên đăng nhập ban đầu
  useEffect(() => {
    const verifySession = async () => {
      if (!token) {
        setAuthChecking(false);
        return;
      }
      try {
        const res = await fetch(`${API_BASE}/api/cp/auth/me`, {
          headers: { 'Authorization': `Bearer ${token}` }
        });
        if (res.ok) {
          const data = await res.json();
          setCurrentUser(data.user);
        } else {
          // Token không hợp lệ hoặc đã hết hạn
          localStorage.removeItem('cp_token');
          setToken('');
          setCurrentUser(null);
        }
      } catch (err) {
        console.error("Auth check failed:", err);
      } finally {
        setAuthChecking(false);
      }
    };
    verifySession();
  }, [token]);

  // Tải dữ liệu hệ thống
  const fetchData = useCallback(async () => {
    if (!token) return;
    try {
      setLoading(true);
      const headers = getAuthHeaders();
      const [ovRes, tRes, mRes, uRes] = await Promise.all([
        fetch(`${API_BASE}/api/cp/overview`, { headers }),
        fetch(`${API_BASE}/api/cp/tenants`, { headers }),
        fetch(`${API_BASE}/api/cp/migrations/status`, { headers }),
        fetch(`${API_BASE}/api/cp/users`, { headers }),
      ]);

      if (ovRes.status === 401 || tRes.status === 401) {
        handleLogout();
        return;
      }

      if (ovRes.ok) setOverview(await ovRes.json());
      if (tRes.ok) {
        const d = await tRes.json();
        const tenantMap = d.tenants || {};
        setTenants(tenantMap);
        // Tự động chọn tenant đầu tiên cho playground nếu chưa chọn hoặc tenant cũ bị xóa
        const tKeys = Object.keys(tenantMap);
        if (tKeys.length > 0 && (!playTenant || !tenantMap[playTenant])) {
          setPlayTenant(tKeys[0]);
        }
      }
      if (mRes.ok) {
        const d = await mRes.json();
        setMigrations(d.statuses || []);
      }
      if (uRes.ok) {
        const d = await uRes.json();
        setCpUsers(d.users || []);
      }
    } catch (err: any) {
      showNotify('error', `Lỗi tải dữ liệu: ${err.message}`);
    } finally {
      setLoading(false);
    }
  }, [token, getAuthHeaders, playTenant]);

  useEffect(() => {
    if (currentUser) {
      fetchData();
    }
  }, [currentUser, fetchData]);

  // Xử lý Đăng nhập Control Plane
  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoginLoading(true);
    setLoginError(null);
    try {
      const res = await fetch(`${API_BASE}/api/cp/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          username: loginUsername.trim(),
          password: loginPassword
        })
      });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || 'Đăng nhập không thành công');
      }
      localStorage.setItem('cp_token', data.token);
      setToken(data.token);
      setCurrentUser(data.user);
      showNotify('success', `Chào mừng ${data.user.full_name} (${data.user.role})!`);
    } catch (err: any) {
      setLoginError(err.message);
    } finally {
      setLoginLoading(false);
    }
  };

  const handleLogout = () => {
    localStorage.removeItem('cp_token');
    setToken('');
    setCurrentUser(null);
    setTenants({});
    setOverview(null);
    setMigrations([]);
    setCpUsers([]);
  };

  // Tạo Tenant mới (Đăng ký tự động & Dynamic DB Provisioning)
  const handleCreateTenant = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanID = newTenantID.toLowerCase().trim().replace(/[^a-z0-9_-]/g, '');
    if (!cleanID || !newTenantName) {
      showNotify('error', 'Vui lòng điền mã Tenant hợp lệ và tên tổ chức');
      return;
    }
    setCreatingTenant(true);
    try {
      const res = await fetch(`${API_BASE}/api/cp/tenants`, {
        method: 'POST',
        headers: getAuthHeaders(),
        body: JSON.stringify({
          tenant_id: cleanID,
          name: newTenantName.trim(),
          plan: newTenantPlan,
          admin_username: newAdminUsername.trim() || 'admin',
          admin_password: newAdminPassword || 'admin123'
        })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Cấp phát Tenant thất bại');
      showNotify('success', `Cấp phát thành công Tenant '${cleanID}'! Đã tạo 2 Database PostgreSQL và khởi tạo Admin User.`);
      setNewTenantID('');
      setNewTenantName('');
      setNewAdminPassword('admin123');
      fetchData();
    } catch (err: any) {
      showNotify('error', err.message);
    } finally {
      setCreatingTenant(false);
    }
  };

  // Xóa Tenant (Xóa DB & Metadata)
  const handleDeleteTenant = async (tenantId: string) => {
    if (!confirm(`CẢNH BÁO: Bạn có chắc chắn muốn XÓA Tenant '${tenantId}'?\n\nThao tác này sẽ DROP VĨNH VIỄN 2 databases 'auth_${tenantId}' và 'parties_${tenantId}'.`)) {
      return;
    }
    try {
      const res = await fetch(`${API_BASE}/api/cp/tenants/${tenantId}`, {
        method: 'DELETE',
        headers: getAuthHeaders(),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Xóa Tenant thất bại');
      showNotify('success', `Đã xóa Tenant '${tenantId}' và thu hồi toàn bộ databases vật lý!`);
      if (playTenant === tenantId) setPlayTenant('');
      fetchData();
    } catch (err: any) {
      showNotify('error', err.message);
    }
  };

  // Tạo CP User mới
  const handleCreateCPUser = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newCpUsername || !newCpPassword || !newCpFullName) {
      showNotify('error', 'Vui lòng nhập đầy đủ thông tin User');
      return;
    }
    setCreatingUser(true);
    try {
      const res = await fetch(`${API_BASE}/api/cp/users`, {
        method: 'POST',
        headers: getAuthHeaders(),
        body: JSON.stringify({
          username: newCpUsername.trim(),
          password: newCpPassword,
          full_name: newCpFullName.trim(),
          email: newCpEmail.trim(),
          role: newCpRole
        })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Tạo User thất bại');
      showNotify('success', `Đã tạo tài khoản quản trị viên '${newCpUsername}'!`);
      setNewCpUsername('');
      setNewCpPassword('');
      setNewCpFullName('');
      setNewCpEmail('');
      fetchData();
    } catch (err: any) {
      showNotify('error', err.message);
    } finally {
      setCreatingUser(false);
    }
  };

  // Xóa CP User
  const handleDeleteCPUser = async (userId: string, username: string) => {
    if (currentUser?.id === userId) {
      showNotify('error', 'Bạn không thể tự xóa tài khoản của chính mình!');
      return;
    }
    if (!confirm(`Xác nhận xóa tài khoản quản trị viên '${username}'?`)) return;
    try {
      const res = await fetch(`${API_BASE}/api/cp/users/${userId}`, {
        method: 'DELETE',
        headers: getAuthHeaders(),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Xóa user thất bại');
      showNotify('success', `Đã xóa user '${username}'`);
      fetchData();
    } catch (err: any) {
      showNotify('error', err.message);
    }
  };

  // Toggle Feature Flag cho Tenant
  const handleToggleFeature = async (tenantId: string, moduleName: string, flag: string, currentVal: boolean) => {
    const t = tenants[tenantId];
    if (!t) return;
    const mod = t.modules[moduleName];
    const newFeatures = { ...(mod?.features || {}), [flag]: !currentVal };

    try {
      const res = await fetch(`${API_BASE}/api/cp/tenants/${tenantId}/modules/${moduleName}`, {
        method: 'PUT',
        headers: getAuthHeaders(),
        body: JSON.stringify({
          connection_string: mod?.connection_string || '',
          features: newFeatures
        })
      });
      if (!res.ok) throw new Error('Không thể cập nhật cấu hình feature');
      showNotify('success', `Đã đổi cờ '${flag}' = ${!currentVal} cho [${tenantId} : ${moduleName}]`);
      fetchData();
    } catch (err: any) {
      showNotify('error', err.message);
    }
  };

  // Chạy Migration Up/Down
  // Script chỉ thêm (add-only): không có rollback
  const handleRunMigration = async (tenantId: string, moduleName: string, action: 'up') => {
    try {
      const res = await fetch(`${API_BASE}/api/cp/migrations/run`, {
        method: 'POST',
        headers: getAuthHeaders(),
        body: JSON.stringify({ tenant_id: tenantId, module_name: moduleName, action })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Thực thi migration thất bại');
      showNotify('success', `Migration ${action.toUpperCase()} cho [${tenantId}:${moduleName}] hoàn tất!`);
      fetchData();
    } catch (err: any) {
      showNotify('error', err.message);
    }
  };

  // Batch Migrate All
  const handleBatchMigrateAll = async () => {
    try {
      const res = await fetch(`${API_BASE}/api/cp/migrations/run-all`, {
        method: 'POST',
        headers: getAuthHeaders()
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Batch migration thất bại');
      showNotify('success', 'Đã hoàn tất đồng bộ migrations cho tất cả Tenant Databases!');
      fetchData();
    } catch (err: any) {
      showNotify('error', err.message);
    }
  };

  // Test API trong Playground
  // useCPToken: gửi token Control Plane (dành cho API chẩn đoán) thay vì JWT của Tenant
  const runTestAPI = async (method: string, path: string, body?: any, useCPToken = false) => {
    if (!playTenant) {
      showNotify('error', 'Vui lòng chọn một Tenant để gửi request');
      return;
    }
    try {
      setPlayOutput(`Đang gửi [${method} ${path}]...\nTenant-Context: ${playTenant}\nPayload: ${body ? JSON.stringify(body, null, 2) : 'none'}`);
      const headers: Record<string, string> = {
        'X-Tenant-ID': playTenant,
      };
      if (body) headers['Content-Type'] = 'application/json';
      const bearer = useCPToken ? token : playTokens[playTenant];
      if (bearer) headers['Authorization'] = `Bearer ${bearer}`;

      const res = await fetch(`${API_BASE}${path}`, {
        method,
        headers,
        body: body ? JSON.stringify(body) : undefined,
      });

      const json = await res.json();
      if (path === '/api/v1/auth/login' && res.ok && json.access_token) {
        setPlayTokens(prev => ({ ...prev, [playTenant]: json.access_token }));
      }
      setPlayOutput(`HTTP ${res.status} ${res.statusText}\nX-Tenant-ID: ${playTenant}\nAuthorization: ${bearer ? 'Bearer ' + bearer.slice(0, 16) + '…' : '(none)'}\nThời gian: ${new Date().toLocaleTimeString()}\n\n` + JSON.stringify(json, null, 2));
    } catch (err: any) {
      setPlayOutput(`Lỗi gọi API: ${err.message}`);
    }
  };

  // MÀN HÌNH ĐĂNG NHẬP (NẾU CHƯA XÁC THỰC)
  if (authChecking) {
    return (
      <div style={{ display: 'flex', height: '100vh', alignItems: 'center', justifyContent: 'center', backgroundColor: '#0b0f19', color: '#9ca3af' }}>
        <RefreshCw className="animate-spin" size={32} style={{ marginRight: '12px', color: '#3b82f6' }} />
        <span>Đang kiểm tra chứng thực Control Plane...</span>
      </div>
    );
  }

  if (!currentUser) {
    return (
      <div style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        backgroundColor: '#0b0f19',
        padding: '20px',
        position: 'relative',
        overflow: 'hidden'
      }}>
        {/* Decorative background glow */}
        <div style={{
          position: 'absolute',
          top: '-15%',
          left: '50%',
          transform: 'translateX(-50%)',
          width: '700px',
          height: '400px',
          background: 'radial-gradient(circle, rgba(59, 130, 246, 0.15) 0%, rgba(11, 15, 25, 0) 70%)',
          pointerEvents: 'none'
        }} />

        <div style={{
          width: '100%',
          maxWidth: '460px',
          backgroundColor: '#111827',
          border: '1px solid #1f2937',
          borderRadius: '16px',
          padding: '36px',
          boxShadow: '0 25px 50px -12px rgba(0, 0, 0, 0.6)',
          zIndex: 1
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '8px' }}>
            <div style={{
              backgroundColor: '#1e3a8a',
              padding: '10px',
              borderRadius: '12px',
              color: '#60a5fa',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center'
            }}>
              <Server size={28} />
            </div>
            <div>
              <h1 style={{ fontSize: '20px', fontWeight: '800', color: '#f9fafb', letterSpacing: '-0.5px' }}>
                AegisKernel Control Plane
              </h1>
              <p style={{ fontSize: '13px', color: '#9ca3af' }}>Hệ thống quản trị Micro-Kernel & Multi-Tenant</p>
            </div>
          </div>

          <p style={{ fontSize: '13.5px', color: '#6b7280', margin: '16px 0 24px', lineHeight: 1.5 }}>
            Đăng nhập để quản trị Tenants độc lập, Dynamic Databases PostgreSQL, phân quyền người dùng và Feature Flags.
          </p>

          {loginError && (
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              padding: '12px 14px',
              backgroundColor: 'rgba(239, 68, 68, 0.1)',
              border: '1px solid #ef4444',
              borderRadius: '8px',
              color: '#f87171',
              fontSize: '13.5px',
              marginBottom: '20px'
            }}>
              <AlertCircle size={18} />
              <span>{loginError}</span>
            </div>
          )}

          <form onSubmit={handleLogin}>
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', fontSize: '12.5px', fontWeight: 600, color: '#d1d5db', marginBottom: '6px' }}>
                Tên đăng nhập
              </label>
              <div style={{ position: 'relative' }}>
                <User size={16} style={{ position: 'absolute', left: '12px', top: '13px', color: '#6b7280' }} />
                <input
                  type="text"
                  required
                  value={loginUsername}
                  onChange={(e) => setLoginUsername(e.target.value)}
                  placeholder="admin"
                  style={{
                    width: '100%',
                    padding: '10px 12px 10px 38px',
                    backgroundColor: '#1f2937',
                    border: '1px solid #374151',
                    borderRadius: '8px',
                    color: '#fff',
                    fontSize: '14px',
                    outline: 'none'
                  }}
                />
              </div>
            </div>

            <div style={{ marginBottom: '24px' }}>
              <label style={{ display: 'block', fontSize: '12.5px', fontWeight: 600, color: '#d1d5db', marginBottom: '6px' }}>
                Mật khẩu
              </label>
              <div style={{ position: 'relative' }}>
                <Lock size={16} style={{ position: 'absolute', left: '12px', top: '13px', color: '#6b7280' }} />
                <input
                  type="password"
                  required
                  value={loginPassword}
                  onChange={(e) => setLoginPassword(e.target.value)}
                  placeholder="••••••••"
                  style={{
                    width: '100%',
                    padding: '10px 12px 10px 38px',
                    backgroundColor: '#1f2937',
                    border: '1px solid #374151',
                    borderRadius: '8px',
                    color: '#fff',
                    fontSize: '14px',
                    outline: 'none'
                  }}
                />
              </div>
            </div>

            <button
              type="submit"
              disabled={loginLoading}
              style={{
                width: '100%',
                padding: '12px',
                backgroundColor: '#2563eb',
                color: '#fff',
                border: 'none',
                borderRadius: '8px',
                fontSize: '14px',
                fontWeight: 600,
                cursor: loginLoading ? 'not-allowed' : 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: '8px',
                transition: 'background 0.2s'
              }}
            >
              {loginLoading ? <RefreshCw className="animate-spin" size={18} /> : <Key size={18} />}
              <span>{loginLoading ? 'Đang xác thực...' : 'Đăng Nhập Control Plane'}</span>
            </button>
          </form>

          <div style={{
            marginTop: '24px',
            paddingTop: '18px',
            borderTop: '1px solid #1f2937',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            fontSize: '12px',
            color: '#9ca3af'
          }}>
            <span>Mặc định khởi tạo:</span>
            <code style={{ backgroundColor: '#1f2937', padding: '3px 8px', borderRadius: '4px', color: '#60a5fa' }}>
              admin / admin123
            </code>
          </div>
        </div>
      </div>
    );
  }

  const tenantKeys = Object.keys(tenants);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', minHeight: '100vh', backgroundColor: '#0b0f19', color: '#f9fafb' }}>
      {/* Toast Notification */}
      {notification && (
        <div style={{
          position: 'fixed',
          top: '20px',
          right: '20px',
          padding: '12px 20px',
          borderRadius: '8px',
          backgroundColor: notification.type === 'success' ? '#065f46' : '#991b1b',
          color: '#fff',
          display: 'flex',
          alignItems: 'center',
          gap: '10px',
          boxShadow: '0 10px 15px -3px rgba(0,0,0,0.5)',
          zIndex: 9999,
          fontWeight: 500,
          fontSize: '14px'
        }}>
          {notification.type === 'success' ? <CheckCircle2 size={18} /> : <AlertCircle size={18} />}
          <span>{notification.message}</span>
        </div>
      )}

      {/* Top Navbar */}
      <header style={{
        backgroundColor: '#111827',
        borderBottom: '1px solid #1f2937',
        padding: '14px 28px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between'
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '14px' }}>
          <div style={{
            backgroundColor: '#1d4ed8',
            color: '#fff',
            padding: '8px',
            borderRadius: '10px',
            display: 'flex'
          }}>
            <Server size={22} />
          </div>
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <span style={{ fontWeight: 800, fontSize: '17px', letterSpacing: '-0.3px' }}>
                AegisKernel Control Plane
              </span>
              <span style={{
                fontSize: '11px',
                fontWeight: 600,
                backgroundColor: 'rgba(16, 185, 129, 0.15)',
                color: '#34d399',
                padding: '2px 8px',
                borderRadius: '12px',
                display: 'inline-flex',
                alignItems: 'center',
                gap: '4px'
              }}>
                <span style={{ width: '6px', height: '6px', borderRadius: '50%', backgroundColor: '#10b981' }}></span>
                {overview?.system_health || 'HEALTHY'}
              </span>
            </div>
            <p style={{ fontSize: '12px', color: '#9ca3af', marginTop: '1px' }}>
              Multi-Tenant Orchestrator & Plug-and-Play Micro-Kernel
            </p>
          </div>
        </div>

        {/* Right User Bar */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '18px' }}>
          {overview && (
            <div style={{ display: 'flex', gap: '16px', fontSize: '12px', color: '#9ca3af', borderRight: '1px solid #374151', paddingRight: '18px' }}>
              <div>Tenants: <strong style={{ color: '#fff' }}>{overview.total_tenants}</strong></div>
              <div>Databases: <strong style={{ color: '#fff' }}>{overview.total_databases}</strong></div>
              <div>Pending Migrations: <strong style={{ color: overview.pending_migrations_dbs > 0 ? '#f59e0b' : '#10b981' }}>{overview.pending_migrations_dbs}</strong></div>
            </div>
          )}

          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            <div style={{
              width: '32px',
              height: '32px',
              borderRadius: '50%',
              backgroundColor: '#374151',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontWeight: 700,
              fontSize: '13px',
              color: '#60a5fa'
            }}>
              {currentUser.username.substring(0, 2).toUpperCase()}
            </div>
            <div style={{ textAlign: 'left', lineHeight: 1.2 }}>
              <div style={{ fontSize: '13px', fontWeight: 600, color: '#f3f4f6' }}>{currentUser.full_name}</div>
              <div style={{ fontSize: '11px', color: '#9ca3af' }}>{currentUser.role}</div>
            </div>
          </div>

          <button
            onClick={handleLogout}
            title="Đăng xuất"
            style={{
              backgroundColor: '#1f2937',
              border: '1px solid #374151',
              color: '#d1d5db',
              padding: '8px 12px',
              borderRadius: '8px',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              fontSize: '12.5px'
            }}
          >
            <LogOut size={15} />
            <span>Thoát</span>
          </button>
        </div>
      </header>

      {/* Main Navigation Tabs */}
      <div style={{
        backgroundColor: '#111827',
        borderBottom: '1px solid #1f2937',
        padding: '0 28px',
        display: 'flex',
        gap: '4px'
      }}>
        <button
          onClick={() => setActiveTab('tenants')}
          style={{
            padding: '14px 18px',
            backgroundColor: 'transparent',
            border: 'none',
            borderBottom: activeTab === 'tenants' ? '2px solid #3b82f6' : '2px solid transparent',
            color: activeTab === 'tenants' ? '#60a5fa' : '#9ca3af',
            fontWeight: 600,
            fontSize: '13.5px',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            gap: '8px'
          }}
        >
          <Building2 size={17} />
          <span>Quản lý Tenants ({tenantKeys.length})</span>
        </button>

        <button
          onClick={() => setActiveTab('users')}
          style={{
            padding: '14px 18px',
            backgroundColor: 'transparent',
            border: 'none',
            borderBottom: activeTab === 'users' ? '2px solid #3b82f6' : '2px solid transparent',
            color: activeTab === 'users' ? '#60a5fa' : '#9ca3af',
            fontWeight: 600,
            fontSize: '13.5px',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            gap: '8px'
          }}
        >
          <Users size={17} />
          <span>Quản trị Users ({cpUsers.length})</span>
        </button>

        <button
          onClick={() => setActiveTab('features')}
          style={{
            padding: '14px 18px',
            backgroundColor: 'transparent',
            border: 'none',
            borderBottom: activeTab === 'features' ? '2px solid #3b82f6' : '2px solid transparent',
            color: activeTab === 'features' ? '#60a5fa' : '#9ca3af',
            fontWeight: 600,
            fontSize: '13.5px',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            gap: '8px'
          }}
        >
          <ToggleRight size={17} />
          <span>Feature Flags</span>
        </button>

        <button
          onClick={() => setActiveTab('migrations')}
          style={{
            padding: '14px 18px',
            backgroundColor: 'transparent',
            border: 'none',
            borderBottom: activeTab === 'migrations' ? '2px solid #3b82f6' : '2px solid transparent',
            color: activeTab === 'migrations' ? '#60a5fa' : '#9ca3af',
            fontWeight: 600,
            fontSize: '13.5px',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            gap: '8px'
          }}
        >
          <Database size={17} />
          <span>Migrations ({migrations.length} DBs)</span>
        </button>

        <button
          onClick={() => setActiveTab('playground')}
          style={{
            padding: '14px 18px',
            backgroundColor: 'transparent',
            border: 'none',
            borderBottom: activeTab === 'playground' ? '2px solid #3b82f6' : '2px solid transparent',
            color: activeTab === 'playground' ? '#60a5fa' : '#9ca3af',
            fontWeight: 600,
            fontSize: '13.5px',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            gap: '8px'
          }}
        >
          <Terminal size={17} />
          <span>Multi-Tenant Playground</span>
        </button>
      </div>

      {/* Main Content Area */}
      <main style={{ flex: 1, padding: '28px', maxWidth: '1400px', width: '100%', margin: '0 auto' }}>
        
        {/* ================= TAB 1: TENANTS ================= */}
        {activeTab === 'tenants' && (
          <div>
            {/* Form đăng ký Tenant Mới */}
            <div style={{
              backgroundColor: '#111827',
              border: '1px solid #1f2937',
              borderRadius: '12px',
              padding: '24px',
              marginBottom: '32px'
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '8px' }}>
                <PlusCircle size={20} style={{ color: '#3b82f6' }} />
                <h2 style={{ fontSize: '16px', fontWeight: 700 }}>
                  Đăng Ký & Khởi Tạo Tenant Mới (Dynamic Database Provisioning)
                </h2>
              </div>
              <p style={{ fontSize: '13px', color: '#9ca3af', marginBottom: '20px', lineHeight: 1.5 }}>
                Tenant được khởi tạo hoàn toàn tự động mà <strong>không có dữ liệu hardcode</strong>. Control Plane sẽ tự động tạo các database PostgreSQL vật lý riêng biệt (<code>auth_&#123;id&#125;</code>, <code>parties_&#123;id&#125;</code>), chạy toàn bộ migrations và khởi tạo tài khoản quản trị đầu tiên cho Tenant.
              </p>

              <form onSubmit={handleCreateTenant} style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '16px', alignItems: 'flex-end' }}>
                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Tenant ID (Slug duy nhất) *
                  </label>
                  <input
                    type="text"
                    required
                    placeholder="vd: alpha-bank, corp-x"
                    value={newTenantID}
                    onChange={(e) => setNewTenantID(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Tên Doanh Nghiệp / Tổ Chức *
                  </label>
                  <input
                    type="text"
                    required
                    placeholder="vd: Ngân hàng số Alpha"
                    value={newTenantName}
                    onChange={(e) => setNewTenantName(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Gói dịch vụ
                  </label>
                  <select
                    value={newTenantPlan}
                    onChange={(e) => setNewTenantPlan(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  >
                    <option value="standard">Standard (Cơ bản - CMND/UserPass)</option>
                    <option value="enterprise">Enterprise (Bảo mật cao - MFA + eKYC CCCD)</option>
                  </select>
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Tenant Admin User
                  </label>
                  <input
                    type="text"
                    placeholder="admin"
                    value={newAdminUsername}
                    onChange={(e) => setNewAdminUsername(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Tenant Admin Pass
                  </label>
                  <input
                    type="password"
                    placeholder="admin123"
                    value={newAdminPassword}
                    onChange={(e) => setNewAdminPassword(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div style={{ display: 'flex', alignItems: 'flex-end' }}>
                  <button
                    type="submit"
                    disabled={creatingTenant}
                    style={{
                      width: '100%',
                      padding: '9px 16px',
                      backgroundColor: '#2563eb',
                      color: '#fff',
                      border: 'none',
                      borderRadius: '6px',
                      fontSize: '13px',
                      fontWeight: 600,
                      cursor: creatingTenant ? 'not-allowed' : 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      gap: '8px'
                    }}
                  >
                    {creatingTenant ? <RefreshCw className="animate-spin" size={16} /> : <PlusCircle size={16} />}
                    <span>{creatingTenant ? 'Đang tạo DBs...' : 'Khởi tạo Tenant'}</span>
                  </button>
                </div>
              </form>
            </div>

            {/* Danh sách Tenants */}
            <div>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '16px' }}>
                <h3 style={{ fontSize: '15px', fontWeight: 700, color: '#e5e7eb' }}>
                  Danh Sách Tenants Đang Hoạt Động ({tenantKeys.length})
                </h3>
                <button
                  onClick={fetchData}
                  disabled={loading}
                  style={{
                    backgroundColor: '#1f2937',
                    border: '1px solid #374151',
                    color: '#9ca3af',
                    padding: '6px 12px',
                    borderRadius: '6px',
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '6px',
                    fontSize: '12px'
                  }}
                >
                  <RefreshCw className={loading ? "animate-spin" : ""} size={14} />
                  <span>Làm mới</span>
                </button>
              </div>

              {tenantKeys.length === 0 ? (
                <div style={{
                  backgroundColor: '#111827',
                  border: '1px dashed #374151',
                  borderRadius: '12px',
                  padding: '48px 24px',
                  textAlign: 'center',
                  color: '#9ca3af'
                }}>
                  <Building2 size={40} style={{ margin: '0 auto 12px', color: '#4b5563' }} />
                  <p style={{ fontSize: '15px', fontWeight: 600, color: '#d1d5db', marginBottom: '6px' }}>
                    Chưa có Tenant nào được đăng ký trong hệ thống
                  </p>
                  <p style={{ fontSize: '13px', color: '#6b7280' }}>
                    Tất cả tenant được quản lý động hoàn toàn, không có tenant mẫu hardcode. Hãy sử dụng form ở trên để cấp phát tenant đầu tiên.
                  </p>
                </div>
              ) : (
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(420px, 1fr))', gap: '20px' }}>
                  {tenantKeys.map(tID => {
                    const t = tenants[tID];
                    const authMod = t.modules?.['auth'];
                    const partiesMod = t.modules?.['parties'];

                    return (
                      <div
                        key={tID}
                        style={{
                          backgroundColor: '#111827',
                          border: '1px solid #1f2937',
                          borderRadius: '12px',
                          padding: '20px',
                          display: 'flex',
                          flexDirection: 'column',
                          justifyContent: 'space-between',
                          boxShadow: '0 4px 6px -1px rgba(0, 0, 0, 0.2)'
                        }}
                      >
                        <div>
                          {/* Card Header */}
                          <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: '12px' }}>
                            <div>
                              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                                <span style={{ fontSize: '16px', fontWeight: 700, color: '#f3f4f6' }}>
                                  {t.name}
                                </span>
                                <span style={{
                                  fontSize: '11px',
                                  fontWeight: 600,
                                  padding: '2px 8px',
                                  borderRadius: '6px',
                                  backgroundColor: t.plan === 'enterprise' ? 'rgba(168, 85, 247, 0.15)' : 'rgba(59, 130, 246, 0.15)',
                                  color: t.plan === 'enterprise' ? '#c084fc' : '#60a5fa'
                                }}>
                                  {t.plan.toUpperCase()}
                                </span>
                              </div>
                              <div style={{ fontSize: '12px', color: '#9ca3af', marginTop: '2px', fontFamily: 'monospace' }}>
                                ID: {tID}
                              </div>
                            </div>

                            <button
                              onClick={() => handleDeleteTenant(tID)}
                              title="Xóa Tenant & Hủy Databases"
                              style={{
                                backgroundColor: 'transparent',
                                border: 'none',
                                color: '#ef4444',
                                cursor: 'pointer',
                                padding: '6px',
                                borderRadius: '6px',
                                display: 'flex'
                              }}
                            >
                              <Trash2 size={16} />
                            </button>
                          </div>

                          {/* Provisioned Databases */}
                          <div style={{
                            backgroundColor: '#0b0f19',
                            border: '1px solid #1f2937',
                            borderRadius: '8px',
                            padding: '12px',
                            fontSize: '12px',
                            marginBottom: '14px'
                          }}>
                            <div style={{ fontWeight: 600, color: '#9ca3af', marginBottom: '8px', display: 'flex', alignItems: 'center', gap: '6px' }}>
                              <Database size={14} style={{ color: '#10b981' }} />
                              <span>Isolated Databases (PostgreSQL):</span>
                            </div>

                            <div style={{ marginBottom: '6px' }}>
                              <span style={{ color: '#38bdf8', fontWeight: 600 }}>auth: </span>
                              <code style={{ fontSize: '11px', color: '#d1d5db' }}>
                                auth_{tID}
                              </code>
                              <span style={{ marginLeft: '6px', fontSize: '10px', color: '#10b981' }}>✓ Admin Initialized</span>
                            </div>

                            <div>
                              <span style={{ color: '#38bdf8', fontWeight: 600 }}>parties: </span>
                              <code style={{ fontSize: '11px', color: '#d1d5db' }}>
                                parties_{tID}
                              </code>
                              <span style={{ marginLeft: '6px', fontSize: '10px', color: '#10b981' }}>✓ Isolated Storage</span>
                            </div>
                          </div>

                          {/* Features Summary */}
                          <div style={{ fontSize: '12px', color: '#9ca3af', marginBottom: '14px' }}>
                            <div style={{ fontWeight: 600, marginBottom: '6px' }}>Features Enabled:</div>
                            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px' }}>
                              {authMod?.features?.['mfa_security'] && (
                                <span style={{ backgroundColor: '#1e3a8a', color: '#93c5fd', padding: '2px 8px', borderRadius: '4px', fontSize: '11px' }}>
                                  mfa_security (Auth)
                                </span>
                              )}
                              {partiesMod?.features?.['ekyc_verification'] && (
                                <span style={{ backgroundColor: '#581c87', color: '#d8b4fe', padding: '2px 8px', borderRadius: '4px', fontSize: '11px' }}>
                                  ekyc_verification (Parties)
                                </span>
                              )}
                              {partiesMod?.features?.['employee_directory'] && (
                                <span style={{ backgroundColor: '#064e3b', color: '#6ee7b7', padding: '2px 8px', borderRadius: '4px', fontSize: '11px' }}>
                                  employee_directory (Parties)
                                </span>
                              )}
                              {!authMod?.features?.['mfa_security'] && !partiesMod?.features?.['ekyc_verification'] && (
                                <span style={{ color: '#6b7280', fontSize: '11px' }}>Standard Business (No override flags)</span>
                              )}
                            </div>
                          </div>
                        </div>

                        {/* Action buttons footer */}
                        <div style={{ display: 'flex', gap: '8px', paddingTop: '12px', borderTop: '1px solid #1f2937' }}>
                          <button
                            onClick={() => {
                              setPlayTenant(tID);
                              setActiveTab('playground');
                            }}
                            style={{
                              flex: 1,
                              padding: '7px 12px',
                              backgroundColor: '#1f2937',
                              border: '1px solid #374151',
                              borderRadius: '6px',
                              color: '#60a5fa',
                              fontSize: '12px',
                              fontWeight: 600,
                              cursor: 'pointer',
                              display: 'flex',
                              alignItems: 'center',
                              justifyContent: 'center',
                              gap: '6px'
                            }}
                          >
                            <Terminal size={14} />
                            <span>Mở trong Playground</span>
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          </div>
        )}

        {/* ================= TAB 2: USERS ADMIN ================= */}
        {activeTab === 'users' && (
          <div>
            {/* Form tạo User mới */}
            <div style={{
              backgroundColor: '#111827',
              border: '1px solid #1f2937',
              borderRadius: '12px',
              padding: '24px',
              marginBottom: '32px'
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '8px' }}>
                <Users size={20} style={{ color: '#3b82f6' }} />
                <h2 style={{ fontSize: '16px', fontWeight: 700 }}>
                  Thêm Quản Trị Viên Control Plane (Admin & Operators)
                </h2>
              </div>
              <p style={{ fontSize: '13px', color: '#9ca3af', marginBottom: '20px' }}>
                Tài khoản Control Plane có quyền cấp phát và thu hồi Tenant, quản lý migrations và thay đổi Feature Flags theo thời gian thực.
              </p>

              <form onSubmit={handleCreateCPUser} style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: '16px', alignItems: 'flex-end' }}>
                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Tên đăng nhập *
                  </label>
                  <input
                    type="text"
                    required
                    placeholder="vd: johndoe"
                    value={newCpUsername}
                    onChange={(e) => setNewCpUsername(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Mật khẩu *
                  </label>
                  <input
                    type="password"
                    required
                    placeholder="••••••••"
                    value={newCpPassword}
                    onChange={(e) => setNewCpPassword(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Họ và tên *
                  </label>
                  <input
                    type="text"
                    required
                    placeholder="vd: John Doe"
                    value={newCpFullName}
                    onChange={(e) => setNewCpFullName(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Email
                  </label>
                  <input
                    type="email"
                    placeholder="john@example.com"
                    value={newCpEmail}
                    onChange={(e) => setNewCpEmail(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '12px', color: '#d1d5db', marginBottom: '6px', fontWeight: 600 }}>
                    Vai trò (Role)
                  </label>
                  <select
                    value={newCpRole}
                    onChange={(e) => setNewCpRole(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '9px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px'
                    }}
                  >
                    <option value="OPERATOR">OPERATOR (Vận hành viên)</option>
                    <option value="SUPER_ADMIN">SUPER_ADMIN (Quản trị tối cao)</option>
                  </select>
                </div>

                <div style={{ display: 'flex', alignItems: 'flex-end' }}>
                  <button
                    type="submit"
                    disabled={creatingUser}
                    style={{
                      width: '100%',
                      padding: '9px 16px',
                      backgroundColor: '#2563eb',
                      color: '#fff',
                      border: 'none',
                      borderRadius: '6px',
                      fontSize: '13px',
                      fontWeight: 600,
                      cursor: creatingUser ? 'not-allowed' : 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      gap: '8px'
                    }}
                  >
                    {creatingUser ? <RefreshCw className="animate-spin" size={16} /> : <UserCheck size={16} />}
                    <span>{creatingUser ? 'Đang tạo...' : 'Tạo Tài Khoản'}</span>
                  </button>
                </div>
              </form>
            </div>

            {/* Danh sách Users Table */}
            <div style={{
              backgroundColor: '#111827',
              border: '1px solid #1f2937',
              borderRadius: '12px',
              overflow: 'hidden'
            }}>
              <div style={{ padding: '16px 20px', borderBottom: '1px solid #1f2937', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <h3 style={{ fontSize: '15px', fontWeight: 700 }}>
                  Danh Sách Quản Trị Viên Control Plane ({cpUsers.length})
                </h3>
              </div>

              <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '13px' }}>
                <thead>
                  <tr style={{ backgroundColor: '#1f2937', color: '#9ca3af' }}>
                    <th style={{ padding: '12px 20px' }}>Tên đăng nhập</th>
                    <th style={{ padding: '12px 20px' }}>Họ và tên</th>
                    <th style={{ padding: '12px 20px' }}>Email</th>
                    <th style={{ padding: '12px 20px' }}>Vai trò</th>
                    <th style={{ padding: '12px 20px' }}>Ngày tạo</th>
                    <th style={{ padding: '12px 20px', textAlign: 'right' }}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {cpUsers.map(u => (
                    <tr key={u.id} style={{ borderBottom: '1px solid #1f2937' }}>
                      <td style={{ padding: '14px 20px', fontWeight: 600, color: '#f3f4f6' }}>
                        {u.username}
                        {currentUser.id === u.id && (
                          <span style={{ marginLeft: '8px', fontSize: '10px', backgroundColor: '#1e3a8a', color: '#93c5fd', padding: '1px 6px', borderRadius: '4px' }}>
                            Bạn
                          </span>
                        )}
                      </td>
                      <td style={{ padding: '14px 20px', color: '#d1d5db' }}>{u.full_name}</td>
                      <td style={{ padding: '14px 20px', color: '#9ca3af' }}>{u.email || '—'}</td>
                      <td style={{ padding: '14px 20px' }}>
                        <span style={{
                          fontSize: '11px',
                          fontWeight: 600,
                          padding: '3px 8px',
                          borderRadius: '6px',
                          backgroundColor: u.role === 'SUPER_ADMIN' ? 'rgba(239, 68, 68, 0.15)' : 'rgba(59, 130, 246, 0.15)',
                          color: u.role === 'SUPER_ADMIN' ? '#f87171' : '#60a5fa'
                        }}>
                          {u.role}
                        </span>
                      </td>
                      <td style={{ padding: '14px 20px', color: '#6b7280', fontSize: '12px' }}>
                        {new Date(u.created_at).toLocaleString()}
                      </td>
                      <td style={{ padding: '14px 20px', textAlign: 'right' }}>
                        {currentUser.id !== u.id ? (
                          <button
                            onClick={() => handleDeleteCPUser(u.id, u.username)}
                            style={{
                              backgroundColor: 'transparent',
                              border: 'none',
                              color: '#ef4444',
                              cursor: 'pointer',
                              padding: '4px 8px',
                              borderRadius: '4px'
                            }}
                          >
                            <Trash2 size={15} />
                          </button>
                        ) : (
                          <span style={{ color: '#4b5563', fontSize: '12px' }}>—</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* ================= TAB 3: FEATURES ================= */}
        {activeTab === 'features' && (
          <div>
            <div style={{ marginBottom: '24px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div>
                <h2 style={{ fontSize: '16px', fontWeight: 700 }}>
                  Quản Lý Feature Flags Theo Từng Tenant
                </h2>
                <p style={{ fontSize: '13px', color: '#9ca3af', marginTop: '2px' }}>
                  Bật/tắt các hành vi nâng cao (Feature Overrides & Extensions) tức thì mà không cần khởi động lại Kernel.
                </p>
              </div>

              {tenantKeys.length > 0 && (
                <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                  <span style={{ fontSize: '13px', color: '#9ca3af' }}>Chọn Tenant:</span>
                  <select
                    value={playTenant}
                    onChange={(e) => setPlayTenant(e.target.value)}
                    style={{
                      padding: '8px 12px',
                      backgroundColor: '#1f2937',
                      border: '1px solid #374151',
                      borderRadius: '6px',
                      color: '#fff',
                      fontSize: '13px',
                      fontWeight: 600
                    }}
                  >
                    {tenantKeys.map(k => (
                      <option key={k} value={k}>{tenants[k].name} ({k})</option>
                    ))}
                  </select>
                </div>
              )}
            </div>

            {tenantKeys.length === 0 ? (
              <div style={{ backgroundColor: '#111827', border: '1px dashed #374151', borderRadius: '12px', padding: '40px', textAlign: 'center', color: '#9ca3af' }}>
                <p>Chưa có Tenant nào. Vui lòng tạo Tenant trong tab 'Quản lý Tenants' trước.</p>
              </div>
            ) : !playTenant || !tenants[playTenant] ? (
              <div style={{ backgroundColor: '#111827', padding: '24px', borderRadius: '12px', textAlign: 'center' }}>
                Vui lòng chọn một Tenant ở trên.
              </div>
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(450px, 1fr))', gap: '24px' }}>
                {/* Module Auth Features */}
                <div style={{ backgroundColor: '#111827', border: '1px solid #1f2937', borderRadius: '12px', padding: '24px' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '16px', borderBottom: '1px solid #1f2937', paddingBottom: '12px' }}>
                    <ShieldCheck size={20} style={{ color: '#3b82f6' }} />
                    <h3 style={{ fontSize: '15px', fontWeight: 700 }}>Module: auth (Xác thực & Bảo mật)</h3>
                  </div>

                  <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
                    {/* MFA Feature */}
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px', backgroundColor: '#0b0f19', borderRadius: '8px', border: '1px solid #1f2937' }}>
                      <div>
                        <div style={{ fontWeight: 600, fontSize: '13.5px', color: '#f3f4f6' }}>mfa_security (Feature Override)</div>
                        <div style={{ fontSize: '12px', color: '#9ca3af', marginTop: '2px' }}>
                          Yêu cầu mã xác thực 2 bước (OTP 654321). Ghi đè quy trình đăng nhập chuẩn.
                        </div>
                      </div>
                      <button
                        onClick={() => handleToggleFeature(playTenant, 'auth', 'mfa_security', !!tenants[playTenant].modules?.['auth']?.features?.['mfa_security'])}
                        style={{
                          backgroundColor: 'transparent',
                          border: 'none',
                          cursor: 'pointer',
                          color: tenants[playTenant].modules?.['auth']?.features?.['mfa_security'] ? '#10b981' : '#6b7280'
                        }}
                      >
                        {tenants[playTenant].modules?.['auth']?.features?.['mfa_security'] ? <ToggleRight size={32} /> : <ToggleLeft size={32} />}
                      </button>
                    </div>
                  </div>
                </div>

                {/* Module Parties Features */}
                <div style={{ backgroundColor: '#111827', border: '1px solid #1f2937', borderRadius: '12px', padding: '24px' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '16px', borderBottom: '1px solid #1f2937', paddingBottom: '12px' }}>
                    <Users size={20} style={{ color: '#a855f7' }} />
                    <h3 style={{ fontSize: '15px', fontWeight: 700 }}>Module: parties (Đối tác & Định danh)</h3>
                  </div>

                  <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
                    {/* eKYC Feature */}
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px', backgroundColor: '#0b0f19', borderRadius: '8px', border: '1px solid #1f2937' }}>
                      <div>
                        <div style={{ fontWeight: 600, fontSize: '13.5px', color: '#f3f4f6' }}>ekyc_verification (Feature Override)</div>
                        <div style={{ fontSize: '12px', color: '#9ca3af', marginTop: '2px' }}>
                          Bắt buộc CCCD gắn chip 12 chữ số. Từ chối CMND 9 số cũ.
                        </div>
                      </div>
                      <button
                        onClick={() => handleToggleFeature(playTenant, 'parties', 'ekyc_verification', !!tenants[playTenant].modules?.['parties']?.features?.['ekyc_verification'])}
                        style={{
                          backgroundColor: 'transparent',
                          border: 'none',
                          cursor: 'pointer',
                          color: tenants[playTenant].modules?.['parties']?.features?.['ekyc_verification'] ? '#10b981' : '#6b7280'
                        }}
                      >
                        {tenants[playTenant].modules?.['parties']?.features?.['ekyc_verification'] ? <ToggleRight size={32} /> : <ToggleLeft size={32} />}
                      </button>
                    </div>

                    {/* Employee Directory Feature */}
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px', backgroundColor: '#0b0f19', borderRadius: '8px', border: '1px solid #1f2937' }}>
                      <div>
                        <div style={{ fontWeight: 600, fontSize: '13.5px', color: '#f3f4f6' }}>employee_directory (Feature Extension)</div>
                        <div style={{ fontSize: '12px', color: '#9ca3af', marginTop: '2px' }}>
                          Mở API /api/v1/parties/employees phục vụ tra cứu danh bạ nội bộ.
                        </div>
                      </div>
                      <button
                        onClick={() => handleToggleFeature(playTenant, 'parties', 'employee_directory', !!tenants[playTenant].modules?.['parties']?.features?.['employee_directory'])}
                        style={{
                          backgroundColor: 'transparent',
                          border: 'none',
                          cursor: 'pointer',
                          color: tenants[playTenant].modules?.['parties']?.features?.['employee_directory'] ? '#10b981' : '#6b7280'
                        }}
                      >
                        {tenants[playTenant].modules?.['parties']?.features?.['employee_directory'] ? <ToggleRight size={32} /> : <ToggleLeft size={32} />}
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>
        )}

        {/* ================= TAB 4: MIGRATIONS ================= */}
        {activeTab === 'migrations' && (
          <div>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '20px' }}>
              <div>
                <h2 style={{ fontSize: '16px', fontWeight: 700 }}>
                  Quản Lý Migrations Cho Nhiều Cơ Sở Dữ Liệu
                </h2>
                <p style={{ fontSize: '13px', color: '#9ca3af', marginTop: '2px' }}>
                  Giám sát phiên bản schema của từng Tenant Database vật lý và đồng bộ migration độc lập.
                </p>
              </div>

              <div style={{ display: 'flex', gap: '10px' }}>
                <button
                  onClick={handleBatchMigrateAll}
                  style={{
                    backgroundColor: '#16a34a',
                    border: 'none',
                    color: '#fff',
                    padding: '8px 14px',
                    borderRadius: '6px',
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '6px',
                    fontSize: '13px',
                    fontWeight: 600
                  }}
                >
                  <ArrowUpCircle size={16} />
                  <span>Đồng Bộ Batch Migrate Tất Cả DBs</span>
                </button>

                <button
                  onClick={fetchData}
                  style={{
                    backgroundColor: '#1f2937',
                    border: '1px solid #374151',
                    color: '#9ca3af',
                    padding: '8px 12px',
                    borderRadius: '6px',
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '6px',
                    fontSize: '13px'
                  }}
                >
                  <RefreshCw className={loading ? "animate-spin" : ""} size={16} />
                  <span>Làm mới</span>
                </button>
              </div>
            </div>

            {migrations.length === 0 ? (
              <div style={{ backgroundColor: '#111827', border: '1px dashed #374151', borderRadius: '12px', padding: '40px', textAlign: 'center', color: '#9ca3af' }}>
                <p>Chưa có thông tin migration nào. Hãy tạo Tenant trong tab Tenants để sinh database tương ứng.</p>
              </div>
            ) : (
              <div style={{ backgroundColor: '#111827', border: '1px solid #1f2937', borderRadius: '12px', overflow: 'hidden' }}>
                <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '13px' }}>
                  <thead>
                    <tr style={{ backgroundColor: '#1f2937', color: '#9ca3af' }}>
                      <th style={{ padding: '12px 18px' }}>Tenant ID</th>
                      <th style={{ padding: '12px 18px' }}>Module & Database</th>
                      <th style={{ padding: '12px 18px' }}>Phiên bản Schema</th>
                      <th style={{ padding: '12px 18px' }}>Trạng thái</th>
                      <th style={{ padding: '12px 18px' }}>Migrations Đã Áp Dụng</th>
                      <th style={{ padding: '12px 18px', textAlign: 'right' }}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {migrations.map((m, idx) => (
                      <tr key={`${m.tenant_id}-${m.module_name}-${idx}`} style={{ borderBottom: '1px solid #1f2937' }}>
                        <td style={{ padding: '14px 18px', fontWeight: 600, color: '#f3f4f6' }}>
                          {m.tenant_id}
                        </td>
                        <td style={{ padding: '14px 18px' }}>
                          <div style={{ fontWeight: 600, color: '#60a5fa' }}>{m.module_name}</div>
                          <code style={{ fontSize: '11px', color: '#9ca3af' }}>
                            {m.module_name}_{m.tenant_id}
                          </code>
                        </td>
                        <td style={{ padding: '14px 18px' }}>
                          <span style={{ fontWeight: 700, color: '#fff' }}>v{m.current_version}</span>
                          <span style={{ color: '#9ca3af' }}> / v{m.latest_version}</span>
                          {m.pending_count > 0 && (
                            <span style={{ marginLeft: '8px', fontSize: '11px', backgroundColor: '#78350f', color: '#fde68a', padding: '1px 6px', borderRadius: '4px' }}>
                              {m.pending_count} pending
                            </span>
                          )}
                        </td>
                        <td style={{ padding: '14px 18px' }}>
                          <span style={{
                            fontSize: '11px',
                            fontWeight: 600,
                            padding: '3px 8px',
                            borderRadius: '6px',
                            backgroundColor: m.status === 'HEALTHY' ? 'rgba(16, 185, 129, 0.15)' : 'rgba(245, 158, 11, 0.15)',
                            color: m.status === 'HEALTHY' ? '#34d399' : '#fbbf24'
                          }}>
                            {m.status}
                          </span>
                        </td>
                        <td style={{ padding: '14px 18px', color: '#9ca3af', fontSize: '12px' }}>
                          {m.applied_migrations?.map(am => (
                            <div key={am.version} style={{ fontFamily: 'monospace', fontSize: '11px' }}>
                              ✓ v{am.version}: {am.name}
                            </div>
                          ))}
                        </td>
                        <td style={{ padding: '14px 18px', textAlign: 'right' }}>
                          <div style={{ display: 'flex', gap: '6px', justifyContent: 'flex-end' }}>
                            <button
                              onClick={() => handleRunMigration(m.tenant_id, m.module_name, 'up')}
                              disabled={m.pending_count === 0}
                              title="Migrate Up"
                              style={{
                                padding: '5px 10px',
                                backgroundColor: m.pending_count > 0 ? '#1d4ed8' : '#374151',
                                color: '#fff',
                                border: 'none',
                                borderRadius: '4px',
                                cursor: m.pending_count > 0 ? 'pointer' : 'not-allowed',
                                fontSize: '11px',
                                display: 'flex',
                                alignItems: 'center',
                                gap: '4px'
                              }}
                            >
                              <ArrowUpCircle size={13} />
                              <span>Up</span>
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}

        {/* ================= TAB 5: PLAYGROUND ================= */}
        {activeTab === 'playground' && (
          <div>
            <div style={{ marginBottom: '20px' }}>
              <h2 style={{ fontSize: '16px', fontWeight: 700 }}>
                Live Multi-Tenant API Playground
              </h2>
              <p style={{ fontSize: '13px', color: '#9ca3af', marginTop: '2px' }}>
                Thử nghiệm tương tác trực tiếp với Kernel Gateway <code>:8080</code> qua header <code>X-Tenant-ID</code>. Quan sát cơ chế Feature Override và Database Isolation.
              </p>
            </div>

            {tenantKeys.length === 0 ? (
              <div style={{ backgroundColor: '#111827', border: '1px dashed #374151', borderRadius: '12px', padding: '40px', textAlign: 'center', color: '#9ca3af' }}>
                <p>Chưa có Tenant nào. Hãy tạo Tenant trong tab Tenants trước khi kiểm thử Playground.</p>
              </div>
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: '1.1fr 1fr', gap: '24px' }}>
                {/* Left Column: API Actions */}
                <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
                  {/* Select Tenant Context */}
                  <div style={{ backgroundColor: '#111827', border: '1px solid #1f2937', borderRadius: '12px', padding: '18px' }}>
                    <label style={{ display: 'block', fontSize: '12px', fontWeight: 700, color: '#38bdf8', marginBottom: '8px' }}>
                      TENANT CONTEXT (Gửi qua Header 'X-Tenant-ID')
                    </label>
                    <select
                      value={playTenant}
                      onChange={(e) => setPlayTenant(e.target.value)}
                      style={{
                        width: '100%',
                        padding: '10px 12px',
                        backgroundColor: '#0b0f19',
                        border: '1px solid #374151',
                        borderRadius: '8px',
                        color: '#fff',
                        fontSize: '14px',
                        fontWeight: 600
                      }}
                    >
                      {tenantKeys.map(k => (
                        <option key={k} value={k}>
                          {tenants[k].name} ({k}) — {tenants[k].plan.toUpperCase()}
                        </option>
                      ))}
                    </select>

                    {playTenant && tenants[playTenant] && (
                      <div style={{ marginTop: '10px', fontSize: '11.5px', color: '#9ca3af', display: 'flex', gap: '12px' }}>
                        <span>MFA: <strong style={{ color: tenants[playTenant].modules?.['auth']?.features?.['mfa_security'] ? '#10b981' : '#ef4444' }}>{tenants[playTenant].modules?.['auth']?.features?.['mfa_security'] ? 'BẬT' : 'TẮT'}</strong></span>
                        <span>eKYC 12 số: <strong style={{ color: tenants[playTenant].modules?.['parties']?.features?.['ekyc_verification'] ? '#10b981' : '#ef4444' }}>{tenants[playTenant].modules?.['parties']?.features?.['ekyc_verification'] ? 'BẬT' : 'TẮT'}</strong></span>
                        <span>JWT: <strong style={{ color: playTokens[playTenant] ? '#10b981' : '#ef4444' }}>{playTokens[playTenant] ? 'ĐÃ ĐĂNG NHẬP' : 'CHƯA (Login trước khi gọi API cần xác thực)'}</strong></span>
                      </div>
                    )}
                  </div>

                  {/* Test Action 1: Đăng nhập Auth */}
                  <div style={{ backgroundColor: '#111827', border: '1px solid #1f2937', borderRadius: '12px', padding: '18px' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '10px' }}>
                      <Key size={16} style={{ color: '#3b82f6' }} />
                      <h4 style={{ fontSize: '14px', fontWeight: 700 }}>1. Test Auth Login (Standard vs MFA Override)</h4>
                    </div>
                    <p style={{ fontSize: '12px', color: '#9ca3af', marginBottom: '14px' }}>
                      Endpoint: <code>POST /api/v1/auth/login</code>. Nếu Tenant bật <code>mfa_security</code>, bắt buộc cung cấp OTP <code>654321</code>.
                    </p>

                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '10px', marginBottom: '12px' }}>
                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>Username</label>
                        <input
                          type="text"
                          value={authUsername}
                          onChange={(e) => setAuthUsername(e.target.value)}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        />
                      </div>
                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>Password</label>
                        <input
                          type="password"
                          value={authPassword}
                          onChange={(e) => setAuthPassword(e.target.value)}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        />
                      </div>
                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>MFA Code (OTP)</label>
                        <input
                          type="text"
                          placeholder="654321"
                          value={authMFACode}
                          onChange={(e) => setAuthMFACode(e.target.value)}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        />
                      </div>
                    </div>

                    <div style={{ display: 'flex', gap: '8px' }}>
                      <button
                        onClick={() => runTestAPI('POST', '/api/v1/auth/login', {
                          username: authUsername,
                          password: authPassword,
                          mfa_code: authMFACode || undefined
                        })}
                        style={{
                          padding: '7px 14px',
                          backgroundColor: '#2563eb',
                          border: 'none',
                          borderRadius: '6px',
                          color: '#fff',
                          fontSize: '12px',
                          fontWeight: 600,
                          cursor: 'pointer',
                          display: 'flex',
                          alignItems: 'center',
                          gap: '6px'
                        }}
                      >
                        <Play size={13} />
                        <span>Thực thi Login</span>
                      </button>

                      <button
                        onClick={() => runTestAPI('GET', '/api/v1/audits?limit=20')}
                        style={{
                          padding: '7px 14px',
                          backgroundColor: '#1f2937',
                          border: '1px solid #374151',
                          borderRadius: '6px',
                          color: '#d1d5db',
                          fontSize: '12px',
                          cursor: 'pointer'
                        }}
                      >
                        <span>Audit Trail (3 tầng)</span>
                      </button>

                      <button
                        onClick={() => runTestAPI('GET', '/api/v1/system/runtime-info', undefined, true)}
                        style={{
                          padding: '7px 14px',
                          backgroundColor: '#1f2937',
                          border: '1px solid #374151',
                          borderRadius: '6px',
                          color: '#d1d5db',
                          fontSize: '12px',
                          cursor: 'pointer'
                        }}
                      >
                        <span>Runtime Info (Hệ thống)</span>
                      </button>

                      <button
                        onClick={() => runTestAPI('GET', '/api/v1/audits/verify-integrity')}
                        style={{
                          padding: '7px 14px',
                          backgroundColor: '#1f2937',
                          border: '1px solid #374151',
                          borderRadius: '6px',
                          color: '#d1d5db',
                          fontSize: '12px',
                          cursor: 'pointer'
                        }}
                      >
                        <span>Verify Hash Chain</span>
                      </button>
                    </div>
                  </div>

                  {/* Test Action 2: Đăng ký Đối Tác */}
                  <div style={{ backgroundColor: '#111827', border: '1px solid #1f2937', borderRadius: '12px', padding: '18px' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '10px' }}>
                      <UserCheck size={16} style={{ color: '#10b981' }} />
                      <h4 style={{ fontSize: '14px', fontWeight: 700 }}>2. Test Đăng Ký Đối Tác (Parties eKYC Override)</h4>
                    </div>
                    <p style={{ fontSize: '12px', color: '#9ca3af', marginBottom: '14px' }}>
                      Endpoint: <code>POST /api/v1/parties</code>. Nếu Tenant bật <code>ekyc_verification</code>, CMND 9 số sẽ bị từ chối và bắt buộc CCCD 12 số.
                    </p>

                    <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr 1.2fr', gap: '10px', marginBottom: '10px' }}>
                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>Họ và tên</label>
                        <input
                          type="text"
                          value={partyFullName}
                          onChange={(e) => setPartyFullName(e.target.value)}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        />
                      </div>
                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>Điện thoại</label>
                        <input
                          type="text"
                          value={partyPhone}
                          onChange={(e) => setPartyPhone(e.target.value)}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        />
                      </div>
                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>Email</label>
                        <input
                          type="email"
                          value={partyEmail}
                          onChange={(e) => setPartyEmail(e.target.value)}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        />
                      </div>
                    </div>

                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.5fr', gap: '10px', marginBottom: '12px' }}>
                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>Loại giấy tờ</label>
                        <select
                          value={partyIDType}
                          onChange={(e) => {
                            setPartyIDType(e.target.value);
                            if (e.target.value === 'CMND') setPartyIDNumber('123456789');
                            else setPartyIDNumber('079090001122');
                          }}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        >
                          <option value="CCCD">CCCD (12 số)</option>
                          <option value="CMND">CMND (9 số)</option>
                        </select>
                      </div>

                      <div>
                        <label style={{ fontSize: '11px', color: '#9ca3af', display: 'block', marginBottom: '4px' }}>Số định danh</label>
                        <input
                          type="text"
                          value={partyIDNumber}
                          onChange={(e) => setPartyIDNumber(e.target.value)}
                          style={{ width: '100%', padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                        />
                      </div>
                    </div>

                    <button
                      onClick={() => runTestAPI('POST', '/api/v1/parties', {
                        type: 'CUSTOMER',
                        full_name: partyFullName,
                        phone: partyPhone,
                        email: partyEmail,
                        identity: {
                          type: partyIDType,
                          number: partyIDNumber,
                          issued_date: '2023-01-15',
                          issued_place: 'Cục CSQLHC về TTXH'
                        }
                      })}
                      style={{
                        padding: '7px 14px',
                        backgroundColor: '#10b981',
                        border: 'none',
                        borderRadius: '6px',
                        color: '#fff',
                        fontSize: '12px',
                        fontWeight: 600,
                        cursor: 'pointer',
                        display: 'flex',
                        alignItems: 'center',
                        gap: '6px'
                      }}
                    >
                      <Play size={13} />
                      <span>Đăng Ký Đối Tác</span>
                    </button>
                  </div>

                  {/* Test Action 3: Tra cứu & Chứng minh Isolation */}
                  <div style={{ backgroundColor: '#111827', border: '1px solid #1f2937', borderRadius: '12px', padding: '18px' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '10px' }}>
                      <Database size={16} style={{ color: '#c084fc' }} />
                      <h4 style={{ fontSize: '14px', fontWeight: 700 }}>3. Kiểm Tra Cô Lập Dữ Liệu (Physical DB Isolation)</h4>
                    </div>
                    <p style={{ fontSize: '12px', color: '#9ca3af', marginBottom: '14px' }}>
                      Tra cứu số định danh bên dưới. Nếu định danh này được lưu ở Tenant khác, Tenant hiện tại sẽ trả về <code>404 Not Found</code>.
                    </p>

                    <div style={{ display: 'flex', gap: '10px' }}>
                      <input
                        type="text"
                        placeholder="Số CCCD / CMND cần tra cứu..."
                        value={queryIDNumber}
                        onChange={(e) => setQueryIDNumber(e.target.value)}
                        style={{ flex: 1, padding: '7px 10px', backgroundColor: '#0b0f19', border: '1px solid #374151', borderRadius: '6px', color: '#fff', fontSize: '12px' }}
                      />
                      <button
                        onClick={() => runTestAPI('GET', `/api/v1/parties/identity?number=${queryIDNumber}`)}
                        style={{
                          padding: '7px 14px',
                          backgroundColor: '#8b5cf6',
                          border: 'none',
                          borderRadius: '6px',
                          color: '#fff',
                          fontSize: '12px',
                          fontWeight: 600,
                          cursor: 'pointer'
                        }}
                      >
                        Tra Cứu
                      </button>
                    </div>
                  </div>
                </div>

                {/* Right Column: Live Terminal Output */}
                <div style={{ backgroundColor: '#050811', border: '1px solid #1f2937', borderRadius: '12px', padding: '20px', display: 'flex', flexDirection: 'column' }}>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '12px', borderBottom: '1px solid #1f2937', paddingBottom: '10px' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                      <Terminal size={16} style={{ color: '#10b981' }} />
                      <span style={{ fontSize: '13px', fontWeight: 700, color: '#e5e7eb' }}>Kernel Gateway Response Monitor</span>
                    </div>
                    <button
                      onClick={() => setPlayOutput('Sẵn sàng nhận request...')}
                      style={{ backgroundColor: 'transparent', border: 'none', color: '#6b7280', fontSize: '11px', cursor: 'pointer' }}
                    >
                      Clear
                    </button>
                  </div>

                  <pre style={{
                    flex: 1,
                    backgroundColor: 'transparent',
                    color: '#34d399',
                    fontSize: '12px',
                    fontFamily: 'JetBrains Mono, monospace',
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word',
                    lineHeight: 1.6,
                    overflowY: 'auto',
                    minHeight: '380px'
                  }}>
                    {playOutput}
                  </pre>
                </div>
              </div>
            )}
          </div>
        )}
      </main>
    </div>
  );
}
