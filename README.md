domain_parameter unit description 

internal_temp °C Internal device or enclosure temperature 
internal_rh % Internal relative humidity

air_temp °C Air temperature at ambient level 
air_rh % Relative humidity in air 
wind_speed m/s Average wind speed over measurement interval 
wind_gust m/s Peak wind speed (gust) during interval 
wind_dir ° Wind direction (0° = North, clockwise) 
rain_depth mm Accumulated rainfall depth solar_rad W/m² Global 
solar radiation (shortwave) 
illuminance lux Ambient light level (illuminance) 
uv_index index Ultraviolet radiation exposure level 
air_pressure hPa Atmospheric (barometric) pressure

external_power bool Power source status (external = true, battery = false)
env_sensor_fail_status bool Environmental sensor health (false = OK, true = failure) 
internal_battery_voltage V Voltage of internal backup battery 
c1_state bool Digital input 1 state (false = open, true = closed) 
c1_count - Pulse count from digital input 1 
c2_state bool Digital input 2 state (false = open, true = closed) 
c2_count - Pulse count from digital input 2

voltage_u_ll_avg V Average line-to-line voltage 
current_i_avg A Average current across phases 
frequency Hz Power system frequency 
power_p_total kW Total active power 
power_q_total kvar Total reactive power 
power_factor - Ratio of active to apparent power 
energy_a_plus kWh Active energy import (import) 
energy_q_plus kvarh Reactive energy import 
energy_a_minus kWh Active energy export 
energy_q_minus kvarh Reactive energy export 
error_code - Device or power quality error code
