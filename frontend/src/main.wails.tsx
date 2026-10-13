// Wails entry: the desktop application (Local mode only).

import { WailsPlatform, WailsService } from './adapters/wails/wails';
import { mount } from './app/mount';

mount(new WailsService(), new WailsPlatform());
