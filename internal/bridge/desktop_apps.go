package bridge

import "agentgo/internal/apps"

func (r *Runtime) desktopApps() *apps.DesktopService {
	if r == nil || r.appsRoot == "" {
		return nil
	}
	reg := func(a apps.InnerApp) {
		if r.capBus != nil {
			r.capBus.Register("app", a.Name, "inner", map[string]string{
				"kind": a.Kind, "app_id": a.ID,
			})
		}
	}
	return &apps.DesktopService{
		Root: r.appsRoot, Store: r.appStore, Pinger: r.appPinger(), OnRegister: reg,
	}
}

func (r *Runtime) appPinger() apps.AppPinger {
	if r == nil {
		return nil
	}
	return &runtimeAppPinger{rt: r}
}
